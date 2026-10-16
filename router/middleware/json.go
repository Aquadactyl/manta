package middleware

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// MaxJSONRequestSize bounds control requests without limiting streamed files,
// uploads, or transfers, which do not use BindJSON.
const MaxJSONRequestSize int64 = 4 << 20

// BindJSON decodes one bounded JSON document and applies Gin's validation.
// It aborts with 413 for oversized bodies and 400 for invalid input.
func BindJSON(c *gin.Context, v any) error {
	var err error
	if c.Request.ContentLength > MaxJSONRequestSize {
		err = &http.MaxBytesError{Limit: MaxJSONRequestSize}
	} else {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxJSONRequestSize)
		dec := json.NewDecoder(c.Request.Body)
		err = dec.Decode(v)
		if err == nil {
			// Require EOF, including after whitespace, so an oversized suffix or
			// a second document cannot bypass the limit.
			var extra json.RawMessage
			if err = dec.Decode(&extra); err == io.EOF {
				err = binding.Validator.ValidateStruct(v)
			} else if err == nil {
				err = errors.New("request body must contain a single JSON document")
			}
		}
	}
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "The JSON request body exceeds the 4 MiB limit."})
		} else {
			_ = c.AbortWithError(http.StatusBadRequest, err).SetType(gin.ErrorTypeBind)
		}
	}
	return err
}
