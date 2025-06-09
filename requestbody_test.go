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
	setupServer := func(t *testing.T, h http.HandlerFunc, globalDefaults ...Option) *httptest.Server {
		t.Helper()

		var handler http.Handler = h
		handler = RequestBodyHandler(handler, globalDefaults...)
		ts := httptest.NewServer(handler)
		t.Cleanup(ts.Close)
		return ts
	}

	echoHandler := func(options ...Option) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			for _, opt := range options {
				SetRequestBodyOption(r, opt)
			}
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("An error occurred"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bodyBytes)
		}
	}

	t.Run("empty no limit", func(t *testing.T) {
		ts := setupServer(t, echoHandler())
		response, err := ts.Client().Get(ts.URL)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("lower handler limit", func(t *testing.T) {
		var limit int64 = 100
		ts := setupServer(t, echoHandler(MaxContentLength(limit)), MaxContentLength(-1))

		response, err := ts.Client().Post(ts.URL, "application/json",
			io.NopCloser(bytes.NewBuffer(make([]byte, limit+1))))

		assert.NoError(t, err)
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
		assert.Equal(t, "413 Request Entity Too Large", response.Status)
		body, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Equal(t, "An error occurred", string(body))
	})
}
