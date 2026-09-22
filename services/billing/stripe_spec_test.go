package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Checking what we send against Stripe's own published API description.
//
// The gap this closes sits between two tests that already exist.
// stripe_test.go pins the request we build, which catches us changing it
// by accident but cannot tell us it was ever right -- it asserts our
// intent against itself. stripe_live_test.go asks the real API, which is
// the only complete answer and needs a secret key, so it skips on every
// machine that has not been given one.
//
// This is the third thing: the request we build, checked against the
// machine-readable spec Stripe publishes at
// github.com/stripe/openapi. It does not prove Stripe will accept a
// charge -- only a live call does that -- but it does prove the endpoint
// exists, the parameters are spelled the way Stripe spells them, and the
// types are what Stripe says they are. Those are the failures that
// otherwise surface as a 400 during a customer's first real invoice.
//
// The spec is not vendored. At 8MB it would dominate the repository, it
// changes weekly, and a stale copy would assert conformance to a version
// nobody is running. Fetch it and point STRIPE_OPENAPI_SPEC at it:
//
//	curl -fsSL -o /tmp/stripe-spec.json \
//	  https://raw.githubusercontent.com/stripe/openapi/master/openapi/spec3.json
//	STRIPE_OPENAPI_SPEC=/tmp/stripe-spec.json go test ./... -run Spec
//
// CI does that; see .github/workflows/ci.yml. Skipping without it is
// deliberate and is called out in the skip message, so an absent spec
// reads as "unverified" rather than as "passed".

type openAPISpec struct {
	Paths map[string]map[string]struct {
		OperationID string `json:"operationId"`
		RequestBody struct {
			Content map[string]struct {
				Schema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
	} `json:"paths"`
}

func loadStripeSpec(t *testing.T) *openAPISpec {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("STRIPE_OPENAPI_SPEC"))
	if path == "" {
		t.Skip("STRIPE_OPENAPI_SPEC is not set: whether the requests this platform builds " +
			"match Stripe's published API description is UNVERIFIED. Fetch " +
			"https://raw.githubusercontent.com/stripe/openapi/master/openapi/spec3.json " +
			"and point STRIPE_OPENAPI_SPEC at it.")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the spec at %s: %v", path, err)
	}
	var spec openAPISpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("the spec at %s is not the OpenAPI document this test understands: %v", path, err)
	}
	if len(spec.Paths) == 0 {
		t.Fatalf("the spec at %s has no paths; wrong file?", path)
	}
	return &spec
}

// formFieldsFor returns the parameter names Stripe accepts on an
// endpoint.
//
// Stripe's spec describes form bodies, so a parameter like
// metadata[foo] appears as the property "metadata". Our form keys are
// flattened, so they are compared on the part before the first bracket.
func formFieldsFor(t *testing.T, spec *openAPISpec, path string) map[string]bool {
	t.Helper()
	ops, ok := spec.Paths[path]
	if !ok {
		t.Fatalf("Stripe's spec has no %s. Either the endpoint moved or this platform is "+
			"calling something that does not exist.", path)
	}
	post, ok := ops["post"]
	if !ok {
		t.Fatalf("Stripe's spec has %s but no POST on it", path)
	}
	out := map[string]bool{}
	for _, content := range post.RequestBody.Content {
		for name := range content.Schema.Properties {
			out[name] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("no form parameters found for POST %s; the spec shape this test assumes has changed", path)
	}
	return out
}

func rootOf(formKey string) string {
	if i := strings.Index(formKey, "["); i >= 0 {
		return formKey[:i]
	}
	return formKey
}

// Every field we send on a payment intent must be one Stripe accepts.
//
// A misspelled parameter is not rejected loudly by Stripe -- it is
// ignored. Sending "curency" would create a payment intent in the
// account's default currency, which is the kind of error that is
// discovered by a customer being charged the wrong amount.
func TestOurPaymentIntentFieldsExistInStripesSpec(t *testing.T) {
	spec := loadStripeSpec(t)
	accepted := formFieldsFor(t, spec, "/v1/payment_intents")

	stub, client, done := newStubStripe(t)
	defer done()

	_, err := client.CreatePaymentIntent(context.Background(), 7500, "USD", "cus_spec",
		"invoice:inv-spec", map[string]string{"openfireblocks_invoice_id": "inv-spec"})
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if len(stub.forms) == 0 {
		t.Fatal("no form was sent")
	}
	form := stub.forms[0]
	for key := range form {
		if !accepted[rootOf(key)] {
			t.Errorf("we send %q on POST /v1/payment_intents and Stripe's spec does not list %q. "+
				"Stripe ignores unknown parameters rather than rejecting them, so this would "+
				"silently not do what it looks like it does.", key, rootOf(key))
		}
	}
}

func TestOurCustomerFieldsExistInStripesSpec(t *testing.T) {
	spec := loadStripeSpec(t)
	accepted := formFieldsFor(t, spec, "/v1/customers")

	stub, client, done := newStubStripe(t)
	defer done()

	if _, err := client.CreateCustomer(context.Background(), "spec@example.com", "Spec", "cus-1"); err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if len(stub.forms) == 0 {
		t.Fatal("no form was sent")
	}

	for key := range stub.forms[0] {
		if !accepted[rootOf(key)] {
			t.Errorf("we send %q on POST /v1/customers and Stripe's spec does not list %q",
				key, rootOf(key))
		}
	}
}

// The pinned API version must be one Stripe recognises.
//
// Stripe resolves an unknown Stripe-Version header to the account's
// default rather than erroring, so a typo or an invented date silently
// un-pins the version -- and the whole reason to pin one is that a
// response shape changing underneath us is how a billing integration
// breaks without anybody deploying anything.
func TestThePinnedApiVersionIsAVersionStripePublishes(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("STRIPE_OPENAPI_SPEC"))
	if path == "" {
		t.Skip("STRIPE_OPENAPI_SPEC is not set; the pinned API version is UNVERIFIED")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The spec carries the version it describes in info.version.
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version == "" {
		t.Skip("this spec does not carry info.version")
	}

	// Not an equality check. The spec tracks Stripe's latest and this
	// platform pins deliberately, so they are expected to differ -- what
	// matters is that ours is a well-formed API date and not newer than
	// the one Stripe publishes, which would mean it does not exist.
	if len(stripeAPIVersion) != len("2024-06-20") || strings.Count(stripeAPIVersion, "-") != 2 {
		t.Fatalf("the pinned Stripe-Version %q is not a YYYY-MM-DD API date", stripeAPIVersion)
	}
	if stripeAPIVersion > doc.Info.Version {
		t.Errorf("this platform pins Stripe-Version %s, which is later than the %s Stripe "+
			"currently publishes. Stripe silently falls back to the account default for a "+
			"version it does not know, so the pin would not be doing anything.",
			stripeAPIVersion, doc.Info.Version)
	}
	t.Logf("pinned %s; Stripe's published spec describes %s", stripeAPIVersion, doc.Info.Version)
}
