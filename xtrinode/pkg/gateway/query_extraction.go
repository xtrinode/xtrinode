package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// Package-level compiled regex for Trino query ID extraction
// Format: YYYYMMDD_HHMMSS_seq_random (e.g., 20250115_123456_00001_abc12)
var trinoQueryIDRe = regexp.MustCompile(`/(\d{8}_\d{6}_\d{5}_[a-zA-Z0-9]+)(?:/|$)`)

// extractQueryIdFromRequest extracts Trino query ID from request URI
// Trino query IDs are in format: YYYYMMDD_HHMMSS_seq_random
// Examples:
//   - /v1/statement/executing/20250115_123456_00001_abc12/...
//   - /v1/query/20250115_123456_00001_abc12
func (gs *GatewayService) extractQueryIdFromRequest(req *http.Request) string {
	path := req.URL.Path

	// Use package-level compiled regex (no recompilation per request)
	matches := trinoQueryIDRe.FindStringSubmatch(path)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

// extractQueryIdFromResponse extracts Trino query ID from JSON response body
// Trino returns query ID in JSON: {"id": "20250115_123456_00001_abc12", ...}
// NOTE: Caller must ensure Content-Type is application/json before calling.
func (gs *GatewayService) extractQueryIdFromResponse(resp *http.Response) string {
	queryID, _ := gs.extractQueryInfoFromResponse(resp)
	return queryID
}

func (gs *GatewayService) extractQueryInfoFromResponse(resp *http.Response) (queryID, state string) {
	// Read only a small prefix (64 KiB) to find the query ID, then restore the full stream.
	// This avoids buffering large responses while preserving the client response body.
	const sniffSize = 64 * 1024
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, sniffSize))
	if err != nil {
		gs.log.V(1).Info("Failed to read response prefix for query ID extraction", "error", err)
		return "", ""
	}

	// Restore full body by prepending the prefix we read
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(prefix), resp.Body))

	queryID, state, err = extractQueryInfoFromJSONPrefix(prefix)
	if err != nil {
		gs.log.V(1).Info("Failed to parse JSON prefix for query ID extraction", "error", err)
	}

	return queryID, state
}

func extractQueryInfoFromJSONPrefix(prefix []byte) (queryID, state string, err error) {
	dec := json.NewDecoder(bytes.NewReader(prefix))
	tok, err := dec.Token()
	if err != nil {
		return "", "", err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return "", "", fmt.Errorf("expected JSON object")
	}

	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			if queryID != "" {
				return queryID, state, nil
			}
			return "", "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			if queryID != "" {
				return queryID, state, nil
			}
			return "", "", fmt.Errorf("expected JSON object key")
		}

		switch key {
		case "id":
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", valueErr
			}
			if id, ok := valueToken.(string); ok {
				queryID = id
			}
		case "state":
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", valueErr
			}
			if topLevelState, ok := valueToken.(string); ok && state == "" {
				state = topLevelState
			}
		case "stats":
			statsState, statsErr := extractStatsState(dec)
			if statsState != "" {
				state = statsState
			}
			if statsErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", statsErr
			}
		default:
			if skipErr := skipJSONValue(dec); skipErr != nil {
				if queryID != "" {
					return queryID, state, nil
				}
				return "", "", skipErr
			}
		}

		if queryID != "" && state != "" {
			return queryID, state, nil
		}
	}

	return queryID, state, nil
}

func extractStatsState(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return "", nil
	}

	state := ""
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			if state != "" {
				return state, nil
			}
			return "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			if state != "" {
				return state, nil
			}
			return "", fmt.Errorf("expected stats object key")
		}
		if key == "state" {
			valueToken, valueErr := dec.Token()
			if valueErr != nil {
				if state != "" {
					return state, nil
				}
				return "", valueErr
			}
			if statsState, ok := valueToken.(string); ok {
				state = statsState
			}
			continue
		}
		if skipErr := skipJSONValue(dec); skipErr != nil {
			if state != "" {
				return state, nil
			}
			return "", skipErr
		}
	}

	if _, err := dec.Token(); err != nil && state == "" {
		return "", err
	}
	return state, nil
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for dec.More() {
			if _, tokenErr := dec.Token(); tokenErr != nil {
				return tokenErr
			}
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return skipErr
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if skipErr := skipJSONValue(dec); skipErr != nil {
				return skipErr
			}
		}
		_, err = dec.Token()
		return err
	default:
		return nil
	}
}
