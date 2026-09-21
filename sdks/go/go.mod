module github.com/openfireblocks/sdk-go

go 1.21

// go-ethereum was declared here and never imported -- see client.go, which
// uses only the standard library and google/uuid. A dependency listed but
// unused still appears in every customer's licence scan, and this one is
// LGPL-3.0, so it was answering questions in procurement reviews for no
// benefit whatsoever.
require github.com/google/uuid v1.5.0
