package core

import (
	"encoding/json"
	"io"
	"net/http"
)

// HTTP helpers for the JSON APIs the phone talks to.

func WriteJSON(writer http.ResponseWriter, status int, v any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(v)
}

func Fail(writer http.ResponseWriter, status int, message string) {
	WriteJSON(writer, status, map[string]string{"error": message})
}

func ReadJSON(req *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(v)
}
