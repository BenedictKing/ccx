package images

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/gin-gonic/gin"
)

func isMultipartContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, "multipart/form-data")
}

func isJSONContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "application/json")
	}
	return strings.EqualFold(mediaType, "application/json")
}

func extractImagesModel(bodyBytes []byte, contentType string) string {
	if isMultipartContentType(contentType) {
		value, _ := extractMultipartField(bodyBytes, contentType, "model")
		return value
	}

	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return ""
	}
	model, _ := reqMap["model"].(string)
	return model
}

func isImagesStreamRequest(c *gin.Context, bodyBytes []byte, contentType string) bool {
	if strings.EqualFold(c.Query("stream"), "true") {
		return true
	}
	if isMultipartContentType(contentType) {
		value, ok := extractMultipartField(bodyBytes, contentType, "stream")
		return ok && strings.EqualFold(value, "true")
	}

	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return false
	}
	return jsonValueIsTrue(reqMap["stream"])
}

func jsonValueIsTrue(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func extractMultipartField(bodyBytes []byte, contentType string, fieldName string) (string, bool) {
	reader, err := newMultipartReader(bodyBytes, contentType)
	if err != nil {
		return "", false
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return "", false
		}
		if err != nil {
			return "", false
		}
		if part.FormName() != fieldName || part.FileName() != "" {
			part.Close()
			continue
		}
		valueBytes, err := io.ReadAll(part)
		part.Close()
		if err != nil {
			return "", false
		}
		return string(valueBytes), true
	}
}

func validateMultipartBody(bodyBytes []byte, contentType string) error {
	reader, err := newMultipartReader(bodyBytes, contentType)
	if err != nil {
		return err
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, part)
		part.Close()
		if readErr != nil {
			return readErr
		}
	}
}

func rewriteMultipartFormField(bodyBytes []byte, contentType string, fieldName string, fieldValue string) ([]byte, string, error) {
	reader, err := newMultipartReader(bodyBytes, contentType)
	if err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	fieldWritten := false

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}

		formName := part.FormName()
		fileName := part.FileName()
		if formName == fieldName && fileName == "" {
			if !fieldWritten {
				if err := writer.WriteField(fieldName, fieldValue); err != nil {
					part.Close()
					return nil, "", err
				}
				fieldWritten = true
			}
			part.Close()
			continue
		}

		if err := copyMultipartPart(writer, part); err != nil {
			part.Close()
			return nil, "", err
		}
		part.Close()
	}

	if !fieldWritten {
		if err := writer.WriteField(fieldName, fieldValue); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	return buf.Bytes(), writer.FormDataContentType(), nil
}

func newMultipartReader(bodyBytes []byte, contentType string) (*multipart.Reader, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("missing multipart boundary")
	}
	return multipart.NewReader(bytes.NewReader(bodyBytes), boundary), nil
}

func copyMultipartPart(writer *multipart.Writer, part *multipart.Part) error {
	header := textproto.MIMEHeader{}
	for key, values := range part.Header {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	newPart, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(newPart, part)
	return err
}
