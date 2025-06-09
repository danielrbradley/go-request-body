package requestbody

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessBody(t *testing.T) {
	t.Parallel()
	setupServer := func(t *testing.T, h http.HandlerFunc) *httptest.Server {
		t.Helper()

		var handler http.Handler = h
		handler = RequestBodyHandler(handler, MaxContentLength(-1))
		ts := httptest.NewServer(handler)
		t.Cleanup(ts.Close)
		return ts
	}

	t.Run("empty no limit", func(t *testing.T) {
		ts := setupServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		response, err := ts.Client().Get(ts.URL)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("modify handler limit", func(t *testing.T) {
		var limit int64 = 100
		ts := setupServer(t, func(w http.ResponseWriter, r *http.Request) {
			// Override the limit in the request context.
			Set(r, MaxContentLength(limit))

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				if _, ok := err.(*http.MaxBytesError); ok {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bodyBytes)
		})

		response, err := ts.Client().Post(ts.URL, "application/json",
			io.NopCloser(bytes.NewBuffer(make([]byte, limit+1))))

		assert.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	})
}
