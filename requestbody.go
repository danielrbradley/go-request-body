package requestbody

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
)

// RequestBodyHandler is middleware for handling content encoding and content length.
// When configuring the handler, you can specify options such as maximum content length,
// supported encodings, and an error handler.
//
// The configuration is only evaluated at the point of reading the request body,
// giving handlers the opportunity to override the defaults on a per-request basis.
//
// Wrapped handlers can override the default options on a per-request basis using
// the `SetRequestBodyOption` function to set options on the request context.
func RequestBodyHandler(h http.Handler, defaults ...Option) http.Handler {
	defaultOptions := options{
		onError:              DefaultOnError,
		requireContentLength: false,
		maxContentLength:     10 * 1024 * 1024, // Default to 10MB
		supportedEncodings: map[string]Encoding{
			"gzip":    {GZipEncodingReader, false},
			"x-gzip":  {GZipEncodingReader, true}, // Alias for gzip
			"deflate": {DeflateEncodingReader, false},
		},
	}
	for _, opt := range defaults {
		opt.apply(&defaultOptions)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			supportedNames := make([]string, 0, len(defaultOptions.supportedEncodings))
			for name := range defaultOptions.supportedEncodings {
				if !defaultOptions.supportedEncodings[name].alias {
					supportedNames = append(supportedNames, name)
				}
			}
			sort.Strings(supportedNames)
			// Advertise supported encodings in the response headers for OPTIONS requests.
			w.Header().Set("Accept-Encoding", strings.Join(supportedNames, ", "))
		}

		// Note: we don't immediately error on content length exceeding the limit,
		// because we want to allow the downstream handler to override the default limits.

		lazyBody := &lazyReader{
			reader:          r.Body,
			contentLength:   r.ContentLength,
			contentEncoding: r.Header.Get("Content-Encoding"),
			options:         defaultOptions,
			request:         r,
			writer:          w,
		}

		r = r.WithContext(context.WithValue(r.Context(), contextKey, &lazyBody.options))
		r.Body = lazyBody

		h.ServeHTTP(w, r)
	})
}

// DefaultOnError is the default error handler for the lazyReader.
// It handles specific errors like MaxBytesError and sets the appropriate HTTP status.
// For other errors, it simply returns the error.
func DefaultOnError(w http.ResponseWriter, r *http.Request, err error) error {
	if bodyError, ok := err.(RequestBodyError); ok {
		w.WriteHeader(bodyError.RecommendedStatusCode())
		return err
	}
	return err
}

func GZipEncodingReader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}
func DeflateEncodingReader(r io.Reader) (io.ReadCloser, error) {
	return flate.NewReader(r), nil
}

type EncodingReader func(r io.Reader) (io.ReadCloser, error)

type RequestBodyError interface {
	Error() string
	RecommendedStatusCode() int
}

type BadRequestError struct {
	Err error
}

func (e *BadRequestError) Error() string {
	return fmt.Sprintf("Bad Request: %v", e.Err)
}
func (e *BadRequestError) RecommendedStatusCode() int {
	return http.StatusBadRequest
}

type RequestContentTooLargeError struct {
	Limit int64
}

func (e *RequestContentTooLargeError) Error() string {
	return fmt.Sprintf("Content Too Large: %d bytes", e.Limit)
}
func (e *RequestContentTooLargeError) RecommendedStatusCode() int {
	return http.StatusRequestEntityTooLarge
}

type RequestContentLengthRequiredError struct {
}

func (e *RequestContentLengthRequiredError) Error() string {
	return "Content Length Required"
}
func (e *RequestContentLengthRequiredError) RecommendedStatusCode() int {
	return http.StatusLengthRequired
}

type RequestUnsupportedMediaTypeError struct {
	Encoding string
}

func (e *RequestUnsupportedMediaTypeError) Error() string {
	return "Unsupported Media Type: " + e.Encoding
}
func (e *RequestUnsupportedMediaTypeError) RecommendedStatusCode() int {
	return http.StatusUnsupportedMediaType
}

type contextType struct{}

var contextKey = contextType{}

type Option interface {
	apply(*options)
}

type options struct {
	maxContentLength     int64
	requireContentLength bool
	supportedEncodings   map[string]Encoding
	onError              func(w http.ResponseWriter, r *http.Request, err error) error
}

type Encoding struct {
	reader EncodingReader
	// alias skips the encoding being advertised in the Accept-Encoding header.
	alias bool
}

func ContentLengthLimit(maxContentLength int64) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.maxContentLength = maxContentLength
		},
	}
}

func OnError(fn func(w http.ResponseWriter, r *http.Request, err error) error) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.onError = fn
		},
	}
}

func RequireContentLength(require bool) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.requireContentLength = require
		},
	}
}

func SupportEncoding(name string, reader EncodingReader) Option {
	return optionFunc{
		f: func(opts *options) {
			if opts.supportedEncodings == nil {
				opts.supportedEncodings = make(map[string]Encoding)
			}
			opts.supportedEncodings[name] = Encoding{
				reader: reader,
				alias:  false,
			}
		},
	}
}

func DisableEncoding(name string) Option {
	return optionFunc{
		f: func(opts *options) {
			if opts.supportedEncodings == nil {
				return // No encodings to disable.
			}
			delete(opts.supportedEncodings, name)
		},
	}
}

func SetRequestBodyOption(r *http.Request, opts ...Option) {
	if r == nil {
		return
	}
	if ctx := r.Context(); ctx != nil {
		if existingOpts, ok := ctx.Value(contextKey).(*options); ok {
			for _, opt := range opts {
				opt.apply(existingOpts)
			}
		}
	}
}

type optionFunc struct {
	f func(*options)
}

func (o optionFunc) apply(opts *options) {
	o.f(opts)
}

type lazyReader struct {
	once            sync.Once
	contentLength   int64
	reader          io.ReadCloser
	contentEncoding string
	initErr         error
	options         options
	request         *http.Request
	writer          http.ResponseWriter
}

func (r *lazyReader) Read(p []byte) (n int, err error) {
	r.init()

	if r.initErr != nil {
		if r.options.onError != nil {
			return 0, r.options.onError(r.writer, r.request, r.initErr)
		}
		return 0, r.initErr
	}

	n, err = r.reader.Read(p)
	if err != nil && err != io.EOF {
		if mbe, ok := err.(*http.MaxBytesError); ok {
			err = &RequestContentTooLargeError{
				Limit: mbe.Limit,
			}
		} else {
			// Wrap other errors in a BadRequestError as we failed while reading the body.
			err = &BadRequestError{
				Err: err,
			}
		}
	}
	if err != nil && r.options.onError != nil {
		return n, r.options.onError(r.writer, r.request, err)
	}
	return n, err
}

func (r *lazyReader) init() {
	r.once.Do(func() {
		if r.contentLength == 0 {
			return // If the content length is zero, we don't need to process the body.
		}

		// Fail if content length not provided but is required.
		if r.contentLength < 0 && r.options.requireContentLength {
			r.initErr = &RequestContentLengthRequiredError{}
			return
		}

		// Fail fast if content length exceeds the maximum allowed limit.
		if r.options.maxContentLength > -1 && r.contentLength > r.options.maxContentLength {
			r.initErr = &RequestContentTooLargeError{
				Limit: r.options.maxContentLength,
			}
			return
		}
		var encodings []EncodingReader
		if r.contentEncoding != "" {
			for _, encoding := range strings.Split(r.contentEncoding, ",") {
				trimmed := strings.TrimSpace(encoding)
				if encoder, supported := r.options.supportedEncodings[trimmed]; supported {
					encodings = append(encodings, encoder.reader)
				} else {
					// If the encoding is not supported, return 415 Unsupported Media Type.
					// https://www.rfc-editor.org/rfc/rfc9110.html#name-415-unsupported-media-type
					r.initErr = &RequestUnsupportedMediaTypeError{
						Encoding: trimmed,
					}
					return
				}
			}
		}

		reader := r.reader
		slices.Reverse(encodings) // Reverse the order to apply the last encoding first.
		// Unwrap each encoding reader in the order they were provided.
		for _, encoding := range encodings {
			// Apply each encoding reader to the reader.
			wrappedReader, err := encoding(reader)
			if err != nil {
				r.initErr = err
				return
			}
			reader = wrappedReader
		}
		if r.options.maxContentLength > 0 {
			// Limit the reader to the specified max content length.
			reader = http.MaxBytesReader(r.writer, reader, r.options.maxContentLength)
		}
		r.reader = reader
	})
}

func (r *lazyReader) Close() error {
	if r.initErr != nil {
		if r.options.onError != nil {
			return r.options.onError(r.writer, r.request, r.initErr)
		}
		return r.initErr
	}
	return r.reader.Close()
}
