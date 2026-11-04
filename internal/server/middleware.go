package server

import (
	"fmt"
	"net/http"
	"slices"
)

type Middleware func(http.Handler) http.Handler

// Chain applies middleware in the given order: the first runs outermost.
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for _, middleware := range slices.Backward(middlewares) {
		handler = middleware(handler)
	}
	return handler
}

// Recover catches panics from the wrapped handler and returns HTTP 500.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			err := recover()
			if err != nil {
				fmt.Printf("Panic recovered from HTTP: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
