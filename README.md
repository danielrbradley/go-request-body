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
      requestbody.ContentLengthLimit(4*1024*1024), // 4MB
      requestbody.RequireContentLength(true))

    http.ListenAndServe(":8000", requestbody.RequestBodyHandler(r))
}

func PostData(w http.ResponseWriter, r *http.Request) {
  // Increase allowed request size for this request
  SetRequestBodyOption(r, requestbody.ContentLengthLimit(100*1024*1024))

  w.WriteHeader(http.StatusOK)
}
```
