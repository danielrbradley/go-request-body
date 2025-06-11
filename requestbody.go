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
		handleError:          StatusOnlyRequestBodyErrorHandler,
		requireContentLength: false,
		maxContentLength:     10 * 1024 * 1024, // Default to 10MB
		supportedEncodings: map[string]encoding{
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

		defer func() {
			if v := recover(); v != nil {
				if bodyError, ok := v.(bodyErrorPanic); ok {
					bodyError.handler(w, r, bodyError.err)
				} else {
					// If it's not a RequestBodyError, re-panic to let it bubble up.
					panic(v)
				}
			}
		}()
		h.ServeHTTP(w, r)
	})
}

func GZipEncodingReader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}
func DeflateEncodingReader(r io.Reader) (io.ReadCloser, error) {
	return flate.NewReader(r), nil
}

type EncodingReader func(r io.Reader) (io.ReadCloser, error)

// RequestBodyError is an interface for errors that can occur while processing the request body.
// Possible errors are: BadRequestError, RequestContentTooLargeError,
// RequestContentLengthRequiredError, and RequestUnsupportedMediaTypeError.
type RequestBodyError interface {
	Error() string
	RecommendedStatusCode() int
}

// BadRequestError is returned when the request body is malformed or cannot be processed.
// The recommended status code for this error is 400 Bad Request.
//
// See: https://www.rfc-editor.org/rfc/rfc9110.html#name-400-bad-request
type BadRequestError struct {
	Err error
}

func (e *BadRequestError) Error() string {
	return fmt.Sprintf("Bad Request: %v", e.Err)
}
func (e *BadRequestError) RecommendedStatusCode() int {
	return http.StatusBadRequest
}

// RequestContentTooLargeError is returned when the request body exceeds the maximum allowed content length.
// The recommended status code for this error is 413 Request Entity Too Large.
//
// See: https://www.rfc-editor.org/rfc/rfc9110.html#name-413-content-too-large
type RequestContentTooLargeError struct {
	Limit int64
}

func (e *RequestContentTooLargeError) Error() string {
	return fmt.Sprintf("Content Too Large: greater than %d bytes", e.Limit)
}
func (e *RequestContentTooLargeError) RecommendedStatusCode() int {
	return http.StatusRequestEntityTooLarge
}

// RequestContentLengthRequiredError is returned when the request does not have a Content-Length header
// set, but the server requires it to be present.
// The recommended status code for this error is 411 Length Required.
//
// See: https://www.rfc-editor.org/rfc/rfc9110.html#name-411-length-required
type RequestContentLengthRequiredError struct {
}

func (e *RequestContentLengthRequiredError) Error() string {
	return "Content Length Required"
}
func (e *RequestContentLengthRequiredError) RecommendedStatusCode() int {
	return http.StatusLengthRequired
}

// RequestUnsupportedMediaTypeError is returned when the request's Content-Encoding
// header contains an encoding that is not supported by the server.
// The recommended status code for this error is 415 Unsupported Media Type.
//
// See: https://www.rfc-editor.org/rfc/rfc9110.html#name-415-unsupported-media-type
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

// Option is a functional option for configuring the request body handler.
// Valid options are ContentLengthLimit, RequireContentLength, SupportEncoding, DisableEncoding,
// HandleRequestBodyError, and ReturnOnError.
type Option interface {
	apply(*options)
}

type options struct {
	maxContentLength     int64
	requireContentLength bool
	supportedEncodings   map[string]encoding
	handleError          RequestBodyErrorHandler
}

type encoding struct {
	reader EncodingReader
	// alias skips the encoding being advertised in the Accept-Encoding header.
	alias bool
}

// ContentLengthLimit sets the maximum content length for the request body.
// If the request body exceeds this limit, a RequestContentTooLargeError will be returned.
// The default limit is 10MB (10 * 1024 * 1024 bytes).
// If you want to disable the limit, use ContentLengthLimit(-1).
func ContentLengthLimit(maxContentLength int64) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.maxContentLength = maxContentLength
		},
	}
}

type RequestBodyErrorHandler func(w http.ResponseWriter, r *http.Request, err RequestBodyError)

// StatusOnlyRequestBodyErrorHandler is the default error handler that only writes the status code
// recommended by the RequestBodyError interface.
func StatusOnlyRequestBodyErrorHandler(w http.ResponseWriter, r *http.Request, err RequestBodyError) {
	w.WriteHeader(err.RecommendedStatusCode())
}

// HandleRequestBodyError will halt request processing using a panic which will be recovered by the middleware
// and use the handler to write a response.
// Passing nil for the handler will disable this behaviour and return the error to the reader of the body which
// is the same as using the ReturnOnError option.
// If not specified, the StatusOnlyRequestBodyErrorHandler will be used.
func HandleRequestBodyError(handler RequestBodyErrorHandler) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.handleError = handler
		},
	}
}

// ReturnOnError will not modify the response, leaving it up to the reader of the
// body to handle RequestBodyError errors.
func ReturnOnError() Option {
	return optionFunc{
		f: func(opts *options) {
			opts.handleError = nil
		},
	}
}

// RequireContentLength will require the request to have a Content-Length header
// set to a non-negative value, if set to true.
func RequireContentLength(require bool) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.requireContentLength = require
		},
	}
}

// SupportEncoding adds a new encoding to the list of supported encodings.
// If the encoding already exists, it will be replaced.
func SupportEncoding(name string, reader EncodingReader) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.supportedEncodings[name] = encoding{
				reader: reader,
				alias:  false,
			}
		},
	}
}

// DisableEncoding removes the specified encoding from the list of supported encodings.
// If the encoding is not supported, it will have no effect.
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

// SetRequestBodyOption sets options for the request body handler on the request context.
// These options will override the default options set in the RequestBodyHandler middleware.
// This allows handlers to customize the behaviour of the request body processing
// on a per-request basis.
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
		return 0, handleError(r.options.handleError, r.initErr)
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
	if err != nil {
		return n, handleError(r.options.handleError, err)
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
				r.initErr = &BadRequestError{
					Err: fmt.Errorf("failed to create encoding reader for %s: %w", r.contentEncoding, err),
				}
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
		return handleError(r.options.handleError, r.initErr)
	}
	return r.reader.Close()
}

type bodyErrorPanic struct {
	err     RequestBodyError
	handler RequestBodyErrorHandler
}

func handleError(handleBodyError RequestBodyErrorHandler, err error) error {
	if handleBodyError != nil {
		if bodyError, ok := err.(RequestBodyError); ok {
			panic(bodyErrorPanic{bodyError, handleBodyError})
		}
	}

	return err
}
