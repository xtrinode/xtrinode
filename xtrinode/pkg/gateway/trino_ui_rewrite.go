package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const trinoUIInfoURIRewritePrefixSize = 64 * 1024

type chainedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (c chainedReadCloser) Close() error {
	return c.closer.Close()
}

func rewriteTrinoUIInfoURI(resp *http.Response) {
	uiPrefix := trinoUIPrefixForResponse(resp)
	if uiPrefix == "" || resp.Body == nil || resp.Body == http.NoBody {
		return
	}
	if !isJSONContentType(resp.Header.Get("Content-Type")) || resp.Header.Get("Content-Encoding") != "" {
		return
	}

	body := resp.Body
	prefix, err := io.ReadAll(io.LimitReader(body, trinoUIInfoURIRewritePrefixSize))
	if err != nil {
		resp.Body = chainedReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), body), closer: body}
		return
	}

	rewrittenPrefix, changed := rewriteTrinoUIInfoURIInJSONPrefix(prefix, uiPrefix)
	resp.Body = chainedReadCloser{Reader: io.MultiReader(bytes.NewReader(rewrittenPrefix), body), closer: body}
	if !changed {
		return
	}
	adjustResponseContentLength(resp, len(rewrittenPrefix)-len(prefix))
}

func trinoUIPrefixForResponse(resp *http.Response) string {
	if resp == nil || resp.Request == nil {
		return ""
	}
	prefix, ok := resp.Request.Context().Value(ctxTrinoUIPrefix).(string)
	if ok && prefix != "" {
		return prefix
	}
	name := resp.Request.Header.Get("X-Trino-XTrinode-Name")
	namespace := resp.Request.Header.Get("X-Trino-XTrinode-Namespace")
	if name == "" || namespace == "" {
		return ""
	}
	return gatewayBackendTrinoUIPath(&Backend{Name: name, Namespace: namespace})
}

func rewriteTrinoUIInfoURIInJSONPrefix(prefix []byte, uiPrefix string) ([]byte, bool) {
	if uiPrefix == "" {
		return prefix, false
	}

	dec := json.NewDecoder(bytes.NewReader(prefix))
	tok, err := dec.Token()
	if err != nil {
		return prefix, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return prefix, false
	}

	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return prefix, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return prefix, false
		}
		if key != "infoUri" {
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return prefix, false
			}
			continue
		}

		valueStart := jsonValueStartAfterKey(prefix, int(dec.InputOffset()))
		if valueStart < 0 {
			return prefix, false
		}
		valueToken, err := dec.Token()
		if err != nil {
			return prefix, false
		}
		infoURI, ok := valueToken.(string)
		if !ok {
			return prefix, false
		}
		rewrittenInfoURI, ok := rewriteTrinoUIInfoURIValue(infoURI, uiPrefix)
		if !ok {
			return prefix, false
		}
		encodedInfoURI, err := json.Marshal(rewrittenInfoURI)
		if err != nil {
			return prefix, false
		}
		valueEnd := int(dec.InputOffset())
		rewritten := make([]byte, 0, len(prefix)+len(encodedInfoURI)-(valueEnd-valueStart))
		rewritten = append(rewritten, prefix[:valueStart]...)
		rewritten = append(rewritten, encodedInfoURI...)
		rewritten = append(rewritten, prefix[valueEnd:]...)
		return rewritten, true
	}

	return prefix, false
}

func jsonValueStartAfterKey(data []byte, keyEnd int) int {
	i := keyEnd
	for i < len(data) && isJSONWhitespace(data[i]) {
		i++
	}
	if i >= len(data) || data[i] != ':' {
		return -1
	}
	i++
	for i < len(data) && isJSONWhitespace(data[i]) {
		i++
	}
	if i >= len(data) {
		return -1
	}
	return i
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

func rewriteTrinoUIInfoURIValue(infoURI, uiPrefix string) (string, bool) {
	parsed, err := url.Parse(infoURI)
	if err != nil {
		return "", false
	}

	prefixPath := strings.TrimRight(uiPrefix, "/")
	if parsed.Path == prefixPath || strings.HasPrefix(parsed.Path, prefixPath+"/") {
		return infoURI, false
	}
	if parsed.Path != TrinoUIPath && parsed.Path != TrinoUIPath+"/" && !isDefaultTrinoUIPath(parsed.Path) {
		return "", false
	}

	suffix := strings.TrimPrefix(parsed.Path, TrinoUIPath)
	if suffix == "" {
		suffix = "/"
	}
	parsed.Path = prefixPath + suffix
	parsed.RawPath = ""
	rewritten := parsed.String()
	return rewritten, rewritten != infoURI
}

func adjustResponseContentLength(resp *http.Response, delta int) {
	if delta == 0 {
		return
	}
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		parsedLength, err := strconv.ParseInt(contentLength, 10, 64)
		newLength := parsedLength + int64(delta)
		if err != nil || newLength < 0 {
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
			return
		}
		resp.Header.Set("Content-Length", strconv.FormatInt(newLength, 10))
		resp.ContentLength = newLength
		return
	}
	if resp.ContentLength > 0 {
		resp.ContentLength += int64(delta)
		return
	}
	resp.ContentLength = -1
}

func isJSONContentType(contentType string) bool {
	return contentType == "application/json" || strings.HasPrefix(contentType, "application/json;")
}
