package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type Map = map[string]any

const MaxBodyBytes = 16 << 20

// BindJSON decodes exactly one JSON value into dst and limits the request body.
func BindJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("request must contain one JSON value")
	}
	return nil
}

// JSON encodes before committing the status so encoding errors leave the response untouched.
func JSON(w http.ResponseWriter, status int, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, err = w.Write(append(data, '\n'))
	return err
}
