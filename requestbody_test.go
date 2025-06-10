package requestbody

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessBody(t *testing.T) {
	t.Parallel()

	t.Run("defaults options", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, nil)
		assert.NoError(t, err)
		response, err := ts.Client().Do(req)

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		assert.Equal(t, "deflate, gzip", response.Header.Get("Accept-Encoding"))
	})

	t.Run("default post at limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		response, err := ts.Client().Post(ts.URL, "application/json",
			bytes.NewBuffer(make([]byte, 10*1024*1024))) // 10MB

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Len(t, responseBody, 10*1024*1024) // 10MB
	})

	t.Run("default post over limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		response, err := ts.Client().Post(ts.URL, "application/json",
			bytes.NewBuffer(make([]byte, 10*1024*1024+1))) // 10MB+1B

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	})

	t.Run("default post gzip encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write(sourceData)
		assert.NoError(t, err)
		assert.NoError(t, gz.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assert.NoError(t, err)
		req.Header.Set("Content-Encoding", "gzip")
		response, err := ts.Client().Do(req)

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Equal(t, sourceData, responseBody)
	})

	t.Run("default post deflate encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		var buf bytes.Buffer
		deflate, err := flate.NewWriter(&buf, flate.BestCompression)
		assert.NoError(t, err)
		_, err = deflate.Write(sourceData)
		assert.NoError(t, err)
		assert.NoError(t, deflate.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assert.NoError(t, err)
		req.Header.Set("Content-Encoding", "deflate")
		response, err := ts.Client().Do(req)

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Equal(t, sourceData, responseBody)
	})

	t.Run("default post deflate+gzip encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		// First write to deflate, then gzip
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		deflate, err := flate.NewWriter(gz, flate.BestCompression)
		assert.NoError(t, err)
		_, err = deflate.Write(sourceData)
		assert.NoError(t, err)
		assert.NoError(t, deflate.Close())
		assert.NoError(t, gz.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assert.NoError(t, err)
		req.Header.Set("Content-Encoding", "deflate, gzip")
		response, err := ts.Client().Do(req)

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Equal(t, sourceData, responseBody)
	})

	t.Run("default gzipped length over limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := make([]byte, 10*1024*1024+1) // 10MB+1B

		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write(sourceData)
		assert.NoError(t, err)
		assert.NoError(t, gz.Close())
		assert.Less(t, buf.Len(), 10*1024*1024) // Gzipped data should be less than 10MB

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assert.NoError(t, err)
		req.Header.Set("Content-Encoding", "gzip")
		response, err := ts.Client().Do(req)

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Len(t, responseBody, 0)
	})

	t.Run("override handler limit", func(t *testing.T) {
		t.Parallel()
		var limit int64 = 100
		handler := echoHandler(ContentLengthLimit(limit))
		ts := setupServer(t, handler, ContentLengthLimit(-1))

		response, err := ts.Client().Post(ts.URL, "application/json",
			io.NopCloser(bytes.NewBuffer(make([]byte, limit+1))))

		assert.NoError(t, err)
		defer response.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		assert.NoError(t, err)
		assert.Equal(t, "", string(body))
	})
}

func setupServer(t *testing.T, h http.HandlerFunc, globalDefaults ...Option) *httptest.Server {
	t.Helper()

	var handler http.Handler = h
	handler = RequestBodyHandler(handler, globalDefaults...)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func echoHandler(options ...Option) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, opt := range options {
			SetRequestBodyOption(r, opt)
		}
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			if _, ok := err.(RequestBodyError); ok {
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("An error occurred"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bodyBytes)
	}
}
