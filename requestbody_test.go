package requestbody

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestProcessBody(t *testing.T) {
	t.Parallel()

	t.Run("defaults options", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, nil)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusOK, response.StatusCode)
		assertEqual(t, "deflate, gzip", response.Header.Get("Accept-Encoding"))
	})

	t.Run("default post at limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		const bodyLength = 10 * 1024 * 1024 // 10MB
		response, err := ts.Client().Post(ts.URL, "application/json",
			bytes.NewBuffer(make([]byte, bodyLength)))

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, bodyLength, len(responseBody))
	})

	t.Run("default post over limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())

		response, err := ts.Client().Post(ts.URL, "application/json",
			bytes.NewBuffer(make([]byte, 10*1024*1024+1))) // 10MB+1B

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	})

	t.Run("default post gzip encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write(sourceData)
		assertNoError(t, err)
		assertNoError(t, gz.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assertNoError(t, err)
		req.Header.Set("Content-Encoding", "gzip")
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, sourceData, responseBody)
	})

	t.Run("default post deflate encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		var buf bytes.Buffer
		deflate, err := flate.NewWriter(&buf, flate.BestCompression)
		assertNoError(t, err)
		_, err = deflate.Write(sourceData)
		assertNoError(t, err)
		assertNoError(t, deflate.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assertNoError(t, err)
		req.Header.Set("Content-Encoding", "deflate")
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, sourceData, responseBody)
	})

	t.Run("default post deflate+gzip encoding", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := []byte("The quick brown fox jumps over the lazy dog")
		// First write to deflate, then gzip
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		deflate, err := flate.NewWriter(gz, flate.BestCompression)
		assertNoError(t, err)
		_, err = deflate.Write(sourceData)
		assertNoError(t, err)
		assertNoError(t, deflate.Close())
		assertNoError(t, gz.Close())

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assertNoError(t, err)
		req.Header.Set("Content-Encoding", "deflate, gzip")
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusOK, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, sourceData, responseBody)
	})

	t.Run("default gzipped length over limit", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler())
		sourceData := make([]byte, 10*1024*1024+1) // 10MB+1B

		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, err := gz.Write(sourceData)
		assertNoError(t, err)
		assertNoError(t, gz.Close())
		if buf.Len() >= 10*1024*1024 {
			t.Errorf("Expected gzipped data to be less than 10MB, got %d bytes", buf.Len())
		}

		req, err := http.NewRequest(http.MethodOptions, ts.URL, io.NopCloser(&buf))
		assertNoError(t, err)
		req.Header.Set("Content-Encoding", "gzip")
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusRequestEntityTooLarge, response.StatusCode)
		responseBody, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, 0, len(responseBody))
	})

	t.Run("require content length missing", func(t *testing.T) {
		t.Parallel()
		ts := setupServer(t, echoHandler(), RequireContentLength(true))

		// New request + io.NopCloser avoids setting length automatically.
		body := io.NopCloser(bytes.NewBuffer(make([]byte, 100)))
		req, err := http.NewRequest(http.MethodPost, ts.URL, body)
		assertNoError(t, err)
		response, err := ts.Client().Do(req)

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusLengthRequired, response.StatusCode)
	})

	t.Run("override handler limit", func(t *testing.T) {
		t.Parallel()
		var limit int64 = 100
		handler := echoHandler(ContentLengthLimit(limit))
		ts := setupServer(t, handler)

		response, err := ts.Client().Post(ts.URL, "application/json",
			io.NopCloser(bytes.NewBuffer(make([]byte, limit+1))))

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusRequestEntityTooLarge, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, "", string(body))
	})

	t.Run("custom error handler", func(t *testing.T) {
		t.Parallel()
		onRequestBodyError := func(w http.ResponseWriter, r *http.Request, err RequestBodyError) {
			w.WriteHeader(http.StatusTeapot) // Custom status code
			_, _ = w.Write([]byte("Custom error: " + err.Error()))
		}
		ts := setupServer(t, echoHandler(), ContentLengthLimit(100), HandleRequestBodyError(onRequestBodyError))

		response, err := ts.Client().Post(ts.URL, "application/json",
			io.NopCloser(bytes.NewBuffer(make([]byte, 101)))) // Over limit

		assertNoError(t, err)
		defer response.Body.Close()
		assertEqual(t, http.StatusTeapot, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		assertNoError(t, err)
		assertEqual(t, "Custom error: Content Too Large: greater than 100 bytes", string(body))
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
		if len(options) > 0 {
			SetRequestBodyOption(r, options...)
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

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("Expected %v, got %v", expected, actual)
	}
}
