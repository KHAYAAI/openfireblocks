package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Who is allowed to become a customer, and how closely they are watched.
//
// Two decisions with regulatory weight and no tests until now: whether a
// KYC profile is acceptable at all, and what risk level it is assigned.
// Onboarding someone who should have been refused is the failure that ends
// up in an enforcement action; assigning "low" to a sanctioned jurisdiction
// is the same failure wearing a different hat.

func validProfile() *KYCProfile {
	return &KYCProfile{
		CustomerID:   "cust-1",
		FullName:     "A Person",
		Email:        "person@example.com",
		Country:      "GB",
		DocumentType: "passport",
		DocumentID:   "P1234567",
		DateOfBirth:  time.Now().AddDate(-30, 0, 0).Format("2006-01-02"),
	}
}

func TestACompleteProfileIsAccepted(t *testing.T) {
	validator := &KYCValidator{}

	if err := validator.validateProfileData(validProfile()); err != nil {
		t.Fatalf("a complete, adult, well-formed profile was refused: %v", err)
	}
}

// Each field is required for a reason, and a profile missing one cannot be
// evidence of anything. Table-driven so that adding a field to the struct
// without adding it here is visible.
func TestEveryRequiredFieldIsRequired(t *testing.T) {
	validator := &KYCValidator{}

	cases := map[string]func(*KYCProfile){
		"customer id":   func(p *KYCProfile) { p.CustomerID = "" },
		"full name":     func(p *KYCProfile) { p.FullName = "" },
		"country":       func(p *KYCProfile) { p.Country = "" },
		"document type": func(p *KYCProfile) { p.DocumentType = "" },
		"document id":   func(p *KYCProfile) { p.DocumentID = "" },
		"date of birth": func(p *KYCProfile) { p.DateOfBirth = "" },
	}

	for missing, remove := range cases {
		t.Run("without a "+missing, func(t *testing.T) {
			profile := validProfile()
			remove(profile)
			if err := validator.validateProfileData(profile); err == nil {
				t.Errorf("a profile with no %s was accepted", missing)
			}
		})
	}
}

// The age check is the one with a hard legal line under it.
func TestSomeoneUnderEighteenIsRefused(t *testing.T) {
	validator := &KYCValidator{}
	profile := validProfile()
	profile.DateOfBirth = time.Now().AddDate(-17, 0, 0).Format("2006-01-02")

	err := validator.validateProfileData(profile)

	if err == nil {
		t.Fatal("a 17-year-old was onboarded")
	}
	if !strings.Contains(err.Error(), "18") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// Just over the line is accepted -- an off-by-one that refuses adults is a
// support burden, and one that accepts minors is a legal problem.
func TestSomeoneJustOverEighteenIsAccepted(t *testing.T) {
	validator := &KYCValidator{}
	profile := validProfile()
	profile.DateOfBirth = time.Now().AddDate(-18, 0, -1).Format("2006-01-02")

	if err := validator.validateProfileData(profile); err != nil {
		t.Errorf("someone who turned 18 yesterday was refused: %v", err)
	}
}

func TestAnUnparseableDateOfBirthIsRefused(t *testing.T) {
	validator := &KYCValidator{}
	profile := validProfile()
	profile.DateOfBirth = "03/03/1990"

	if err := validator.validateProfileData(profile); err == nil {
		t.Error("a date of birth in an unrecognised format was accepted, so no age was checked")
	}
}

func TestAMalformedEmailIsRefused(t *testing.T) {
	validator := &KYCValidator{}

	for _, address := range []string{"", "not-an-email", "@example.com", "person@"} {
		t.Run(fmt.Sprintf("%q", address), func(t *testing.T) {
			profile := validProfile()
			profile.Email = address
			if err := validator.validateProfileData(profile); err == nil {
				t.Errorf("%q was accepted as an email address", address)
			}
		})
	}
}

// Comprehensively sanctioned jurisdictions are high risk regardless of
// what document was presented. A passport from a sanctioned country is
// still a customer from a sanctioned country.
func TestSanctionedJurisdictionsAreHighRisk(t *testing.T) {
	validator := &KYCValidator{}

	for _, country := range []string{"KP", "IR", "SY"} {
		t.Run(country, func(t *testing.T) {
			profile := validProfile()
			profile.Country = country
			profile.DocumentType = "passport" // the lowest-risk document

			if level := validator.assessRiskLevel(profile); level != "high" {
				t.Errorf("a customer from %s was assessed %q, not high", country, level)
			}
		})
	}
}

func TestADriverLicenceIsMediumRisk(t *testing.T) {
	validator := &KYCValidator{}
	profile := validProfile()
	profile.DocumentType = "driver_license"

	if level := validator.assessRiskLevel(profile); level != "medium" {
		t.Errorf("a driver licence was assessed %q, want medium", level)
	}
}

// Document type is matched case-insensitively, because it arrives from an
// API caller and "Driver_License" is the same document.
func TestDocumentTypeMatchingIgnoresCase(t *testing.T) {
	validator := &KYCValidator{}

	for _, spelling := range []string{"driver_license", "DRIVER_LICENSE", "Driver_License"} {
		profile := validProfile()
		profile.DocumentType = spelling
		if level := validator.assessRiskLevel(profile); level != "medium" {
			t.Errorf("%q was assessed %q, want medium", spelling, level)
		}
	}
}

func TestAPassportFromAnUnremarkableCountryIsLowRisk(t *testing.T) {
	validator := &KYCValidator{}

	if level := validator.assessRiskLevel(validProfile()); level != "low" {
		t.Errorf("a GB passport was assessed %q, want low", level)
	}
}

// An unrecognised document type must not silently land in the lowest
// bucket... which today it does. This test documents the current behaviour
// and says why it is worth changing: the risk model treats "we do not know
// what this document is" as equivalent to a passport.
func TestAnUnknownDocumentTypeIsCurrentlyTreatedAsLowRisk(t *testing.T) {
	validator := &KYCValidator{}
	profile := validProfile()
	profile.DocumentType = "library_card"

	level := validator.assessRiskLevel(profile)

	if level != "low" {
		t.Fatalf("behaviour changed: an unknown document type is now %q", level)
	}
	t.Log("an unrecognised document type is assessed low risk, the same as a passport; " +
		"defaulting unknown documents to medium would be the more defensible model")
}
