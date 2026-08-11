package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mapCallResult converts an MCP tool result into the JSON payload a coder sees.
//
// MCP results are richer than a connector's JSON body — a call may return text,
// structured JSON, images, audio, resource links or embedded resources, in any
// combination — so the reduction rule has to be explicit rather than incidental:
//
//  1. structuredContent wins when present. It is already JSON conforming to the
//     tool's outputSchema, which is strictly better for a model than prose.
//  2. Otherwise the text blocks are concatenated.
//  3. Image and audio blocks are REPLACED BY A PLACEHOLDER naming the mime type.
//     This is the load-bearing one: a single screenshot's base64 runs to hundreds of
//     kilobytes and would consume the entire 8 KiB result budget while teaching the
//     model nothing it can act on. The placeholder at least tells it an image came
//     back.
//  4. Resource links keep uri + name, which is what a follow-up call needs.
func mapCallResult(out *sdk.CallToolResult) (Result, error) {
	if out == nil {
		return Result{Data: json.RawMessage(`"(no result)"`)}, nil
	}

	if out.StructuredContent != nil {
		b, err := json.Marshal(out.StructuredContent)
		if err == nil {
			return Result{Data: b, IsError: out.IsError}, nil
		}
		// Fall through to the text path rather than failing: a server whose
		// structured content will not round-trip still usually sent readable text.
	}

	var sb strings.Builder
	for _, c := range out.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(v.Text)
		case *sdk.ImageContent:
			writePlaceholder(&sb, "image", v.MIMEType, len(v.Data))
		case *sdk.AudioContent:
			writePlaceholder(&sb, "audio", v.MIMEType, len(v.Data))
		case *sdk.ResourceLink:
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			name := v.Name
			if name == "" {
				name = v.Title
			}
			sb.WriteString(fmt.Sprintf("[resource: %s <%s>]", name, v.URI))
		case *sdk.EmbeddedResource:
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			switch {
			case v.Resource == nil:
				sb.WriteString("[embedded resource]")
			case v.Resource.Text != "":
				sb.WriteString(v.Resource.Text)
			default:
				sb.WriteString(fmt.Sprintf("[embedded resource %s (%s), %d bytes of binary omitted]",
					v.Resource.URI, v.Resource.MIMEType, len(v.Resource.Blob)))
			}
		}
	}

	text := sb.String()
	if strings.TrimSpace(text) == "" {
		// Never return an empty result: a strict provider serializer rejects an
		// empty tool result outright, and the model reads an empty string as a
		// failure it should retry.
		text = "(tool returned no content)"
	}
	b, err := json.Marshal(text)
	if err != nil {
		return Result{}, errf(KindOther, "could not encode MCP result: "+err.Error())
	}
	return Result{Data: b, IsError: out.IsError}, nil
}

func writePlaceholder(sb *strings.Builder, kind, mime string, n int) {
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	if mime == "" {
		mime = "unknown"
	}
	sb.WriteString(fmt.Sprintf("[%s omitted: %s, %d bytes]", kind, mime, n))
}
