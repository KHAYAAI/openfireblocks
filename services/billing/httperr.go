package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
)

// Turning internal failures into honest HTTP status codes.
//
// These handlers used to answer any error with 500 and the error's text.
// Two things were wrong with that, and both showed up the first time
// anything actually called them (infrastructure/kind/services-smoke-test.sh):
//
//   - A request naming a resource that does not exist is not a server
//     failure. Answering 500 tells the caller to retry something that will
//     never work, and pages whoever owns the alerts.
//   - The text came straight from the driver. A malformed id produced
//     `pq: invalid input syntax for type uuid: "" (22P02)` in the response
//     body, handing an unauthenticated caller the database engine, the
//     column type and the SQLSTATE.
//
// So: classify, answer accordingly, and keep the detail in the logs where
// an operator can see it and a caller cannot.
var (
	// ErrNotFound means the caller named something that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalidInput means the request was malformed.
	ErrInvalidInput = errors.New("invalid input")
	// ErrUnavailable means a dependency this service needs is missing or
	// unreachable -- nothing is wrong with the request and retrying later
	// may work. Payments are the case that matters: "no payment processor
	// is configured" answered as a 500 reads as a crash, when it is a
	// deployment that was never finished.
	ErrUnavailable = errors.New("unavailable")
)

// uuidPattern is deliberately strict. Postgres rejects a malformed uuid with
// a driver error that would otherwise become a 500, so the shape is checked
// before the query rather than after it fails.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// requireUUID validates a required identifier from a query string.
func requireUUID(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, name)
	}
	if !uuidPattern.MatchString(value) {
		return fmt.Errorf("%w: %s must be a UUID", ErrInvalidInput, name)
	}
	return nil
}

// writeError answers with the right status and a message safe to return.
//
// context is what the caller was trying to do, in the caller's terms. The
// underlying error is logged in full and never sent.
func writeError(w http.ResponseWriter, context string, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		// Safe to return: these messages are written here, not by a driver.
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNotFound):
		http.Error(w, context+": not found", http.StatusNotFound)
	case errors.Is(err, ErrUnavailable):
		log.Printf("%s: %v", context, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		log.Printf("%s: %v", context, err)
		http.Error(w, context+": internal error", http.StatusInternalServerError)
	}
}
