package main

import (
	"encoding/json"
	"strings"
	"unsafe"
)

type WasmHTTPRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type WasmHTTPResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

var requestBuffer []byte
var responseBuffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	requestBuffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&requestBuffer[0])))
}

//go:wasmexport handler
func handler(offset uint32, length uint32) uint64 {
	reqBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(offset))), length)

	var req WasmHTTPRequest
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		return serializeResponse(&WasmHTTPResponse{
			Status:  400,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
			Body:    "Invalid request JSON",
		})
	}

	// We only accept POST requests
	if req.Method != "POST" {
		return serializeResponse(&WasmHTTPResponse{
			Status:  405,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
			Body:    "Method Not Allowed. Use POST.",
		})
	}

	// Simple markdown to HTML parser logic
	htmlOutput := convertMarkdownToHTML(req.Body)

	resp := &WasmHTTPResponse{
		Status: 200,
		Headers: map[string][]string{
			"Content-Type": {"text/html; charset=utf-8"},
			"X-Service":    {"wasm-markdown-parser"},
		},
		Body: htmlOutput,
	}

	return serializeResponse(resp)
}

func convertMarkdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	var htmlLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Header 1: # Header
		if strings.HasPrefix(trimmed, "# ") {
			htmlLines = append(htmlLines, "<h1>"+strings.TrimPrefix(trimmed, "# ")+"</h1>")
			continue
		}

		// Header 2: ## Header
		if strings.HasPrefix(trimmed, "## ") {
			htmlLines = append(htmlLines, "<h2>"+strings.TrimPrefix(trimmed, "## ")+"</h2>")
			continue
		}

		// Bold: **text** to <strong>text</strong>
		lineText := trimmed
		for {
			start := strings.Index(lineText, "**")
			if start == -1 {
				break
			}
			end := strings.Index(lineText[start+2:], "**")
			if end == -1 {
				break
			}
			actualEnd := start + 2 + end
			boldText := lineText[start+2 : actualEnd]
			lineText = lineText[:start] + "<strong>" + boldText + "</strong>" + lineText[actualEnd+2:]
		}

		// Italic: *text* to <em>text</em>
		for {
			start := strings.Index(lineText, "*")
			if start == -1 {
				break
			}
			end := strings.Index(lineText[start+1:], "*")
			if end == -1 {
				break
			}
			actualEnd := start + 1 + end
			italicText := lineText[start+1 : actualEnd]
			lineText = lineText[:start] + "<em>" + italicText + "</em>" + lineText[actualEnd+1:]
		}

		htmlLines = append(htmlLines, "<p>"+lineText+"</p>")
	}

	return strings.Join(htmlLines, "\n")
}

func serializeResponse(resp *WasmHTTPResponse) uint64 {
	respBytes, err := json.Marshal(resp)
	if err != nil {
		fallback := []byte(`{"status": 500, "headers": {}, "body": "internal guest marshalling error"}`)
		responseBuffer = fallback
	} else {
		responseBuffer = respBytes
	}

	respOffset := uint32(uintptr(unsafe.Pointer(&responseBuffer[0])))
	respLength := uint32(len(responseBuffer))

	return (uint64(respOffset) << 32) | uint64(respLength)
}

func main() {}
