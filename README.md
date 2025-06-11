# Go Request Body Middleware

RequestBodyHandler is middleware for handling content encoding and limiting allowed content length.

When configuring the middleware, you can specify options such as maximum content length, supported encodings, and an error handler. By default, gzip and deflate encodings are supported and the request content length is limited to 10MB.

Configuration can be overridden by handlers on a per-request basis.

```go
import (
    "net/http"
    "github.com/danielrbradley/go-request-body"
)

func main() {
    r := http.NewServeMux()

    r.HandleFunc("/", PostData)

    // Wrap our server to handle compressed requests with default limits.
    middleware := requestbody.RequestBodyHandler(r,
      // Set a custom limit of 4MB instead of the default 10MB
      requestbody.ContentLengthLimit(4*1024*1024),
      // Enforce clients setting content length so we can fail fast before reading up to the limit (not required by default)
      requestbody.RequireContentLength(true))

    http.ListenAndServe(":8000", requestbody.RequestBodyHandler(r))
}

// PostData is an example HTTP handler
func PostData(w http.ResponseWriter, r *http.Request) {
  // Increase allowed request size for this request
  requestbody.SetRequestBodyOption(r, requestbody.ContentLengthLimit(100*1024*1024))

  w.WriteHeader(http.StatusOK)
}
```

## Default Behaviour

- The default content length limit is 10MB. This can be modified using the `requestbody.ContentLengthLimit(maxContentLength int64)` option.
- The content length request header is not required by default but can be modified using the `requestbody.RequireContentLength(require bool)` option.
- The default error behaviour is to set an appropriate status code on the response then return the error to the reader of the body. The error behaviour can be modified by using the `requestbody.OnError(fn func(w http.ResponseWriter, r *http.Request, err error) error)` option.
- The default supported encodings are "gzip" (also aliased as "x-gzip") and "deflate". These can be disabled using the `DisableEncoding(name string)` option or custom encodings specified using the `SupportEncoding(name string, reader EncodingReader)` option.

## Error Handling

When the RequestBodyHandler is being used, reading from the `http.Request` `Body` can only throw either `io.EOF` or `requestbody.RequestBodyError`. The default error handler (`SetStatusOnError`) will write the recommended status code from any `requestbody.RequestBodyError` to the `http.ResponseWriter` `WriteHeader(statusCode int)` method. This behaviour can be modified by setting the `OnError` option:

```go
func PostData(w http.ResponseWriter, r *http.Request) {
  // Increase allowed request size for this request
  requestbody.SetRequestBodyOption(r, requestbody.OnError(requestbody.PassThroughOnError))

  responseBody, err := io.ReadAll(response.Body)
  if err != nil {
    // Write custom status code handling inline
  }
  // ...
}
```
