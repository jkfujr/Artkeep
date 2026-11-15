package server

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPI []byte

func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, _ = w.Write(openAPI)
	}
}
