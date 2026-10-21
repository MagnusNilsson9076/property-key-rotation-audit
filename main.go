package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
)

func main() {
	client, err := NewInfraiClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /property-key-rotations", func(w http.ResponseWriter, r *http.Request) {
		var input RotationInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		report, err := client.RotateAndAudit(r.Context(), input)
		if err != nil {
			status := http.StatusBadGateway
			var remote *apiError
			if errors.As(err, &remote) {
				status = http.StatusUnprocessableEntity
			} else if err := readyForRotation(input); err != nil {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(report)
	})
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("property key rotation service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
