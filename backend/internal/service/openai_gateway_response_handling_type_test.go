package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIStreamEventIsTerminalWithTypeMatchesExistingSemantics(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "empty", data: "", want: false},
		{name: "whitespace", data: " \t ", want: false},
		{name: "done", data: " [DONE] ", want: true},
		{name: "JSON outer whitespace", data: " \n\t {\"type\":\"response.completed\"} \r\n", want: true},
		{name: "completed", data: `{"type":"response.completed"}`, want: true},
		{name: "response done", data: `{"type":"response.done"}`, want: true},
		{name: "failed", data: `{"type":"response.failed"}`, want: true},
		{name: "incomplete", data: `{"type":"response.incomplete"}`, want: true},
		{name: "cancelled", data: `{"type":"response.cancelled"}`, want: true},
		{name: "canceled", data: `{"type":"response.canceled"}`, want: true},
		{name: "delta", data: `{"type":"response.output_text.delta"}`, want: false},
		{name: "invalid JSON", data: `{"type":`, want: false},
		{name: "terminal with trailing garbage", data: `{"type":"response.completed"} trailing`, want: true},
		{name: "nonterminal with trailing garbage", data: `{"type":"response.output_text.delta"} trailing`, want: false},
		{name: "type whitespace is normalized", data: `{"type":" response.completed "}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventType := gjson.GetBytes([]byte(tt.data), "type").String()
			got := openAIStreamEventIsTerminalWithType(tt.data, eventType)

			require.Equal(t, tt.want, got)
			require.Equal(t, openAIStreamEventIsTerminal(tt.data), got)
		})
	}
}

func TestOpenAIStreamDataStartsClientOutputUsesSemanticPayload(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		want      bool
	}{
		{name: "created is preamble", eventType: "response.created", data: `{"type":"response.created"}`, want: false},
		{name: "empty message item is structural", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"message","content":[]}}`, want: false},
		{name: "message item with text is semantic", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"message","content":[{"type":"output_text","text":"hello"}]}}`, want: true},
		{name: "empty content part is structural", eventType: "response.content_part.added", data: `{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`, want: false},
		{name: "content part with text is semantic", eventType: "response.content_part.added", data: `{"type":"response.content_part.added","part":{"type":"output_text","text":"hello"}}`, want: true},
		{name: "empty text delta is structural", eventType: "response.output_text.delta", data: `{"type":"response.output_text.delta","delta":""}`, want: false},
		{name: "text delta is semantic", eventType: "response.output_text.delta", data: `{"type":"response.output_text.delta","delta":"hello"}`, want: true},
		{name: "empty function arguments delta is structural", eventType: "response.function_call_arguments.delta", data: `{"type":"response.function_call_arguments.delta","delta":""}`, want: false},
		{name: "function call item is semantic", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"function_call","name":"exec_command","arguments":""}}`, want: true},
		{name: "encrypted reasoning is staged", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"reasoning","encrypted_content":"opaque"}}`, want: false},
		{name: "visible reasoning summary is semantic", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[{"text":"summary"}]}}`, want: true},
		{name: "refusal is semantic", eventType: "response.output_item.added", data: `{"type":"response.output_item.added","item":{"type":"message","content":[{"type":"refusal","refusal":"cannot comply"}]}}`, want: true},
		{name: "terminal event is delivered", eventType: "response.completed", data: `{"type":"response.completed","response":{"output":[]}}`, want: true},
		{name: "done marker is structural", data: `[DONE]`, want: false},
		{name: "unknown event remains conservative", eventType: "response.future_event", data: `{"type":"response.future_event"}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIStreamDataStartsClientOutput(tt.data, tt.eventType))
		})
	}
}

func TestOpenAIStreamTerminalShouldFailoverRequiresOutputOrUsage(t *testing.T) {
	tests := []struct {
		name                string
		data                string
		clientOutputStarted bool
		want                bool
	}{
		{name: "missing output and usage", data: `{"type":"response.completed","response":{}}`, want: true},
		{name: "empty output and missing usage", data: `{"type":"response.completed","response":{"output":[]}}`, want: true},
		{name: "empty usage object", data: `{"type":"response.completed","response":{"output":[],"usage":{}}}`, want: true},
		{name: "zero usage object remains retryable", data: `{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0}}}`, want: true},
		{name: "response usage is valid", data: `{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":1,"output_tokens":0}}}`, want: false},
		{name: "cached usage is valid", data: `{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0,"input_tokens_details":{"cached_tokens":1}}}}`, want: false},
		{name: "top level usage is valid", data: `{"type":"response.done","usage":{"input_tokens":1,"output_tokens":0}}`, want: false},
		{name: "tool usage is valid", data: `{"type":"response.completed","response":{"tool_usage":{"image_gen":{"images":1}}}}`, want: false},
		{name: "message output is valid", data: `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`, want: false},
		{name: "tool call output is valid", data: `{"type":"response.completed","response":{"output":[{"type":"function_call","name":"exec_command","arguments":"{}"}]}}`, want: false},
		{name: "hidden reasoning without usage remains retryable", data: `{"type":"response.completed","response":{"output":[{"type":"reasoning","encrypted_content":"opaque"}]}}`, want: true},
		{name: "empty incomplete remains retryable", data: `{"type":"response.incomplete","response":{"output":[]}}`, want: true},
		{name: "empty cancelled remains retryable", data: `{"type":"response.cancelled","response":{"output":[]}}`, want: true},
		{name: "empty canceled remains retryable", data: `{"type":"response.canceled","response":{"output":[]}}`, want: true},
		{name: "incomplete with usage is not replayed", data: `{"type":"response.incomplete","response":{"output":[],"usage":{"input_tokens":1,"output_tokens":0}}}`, want: false},
		{name: "prior client output prevents replay", data: `{"type":"response.completed","response":{"output":[]}}`, clientOutputStarted: true, want: false},
		{name: "non-success terminal is handled elsewhere", data: `{"type":"response.failed","response":{}}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventType := gjson.Get(tt.data, "type").String()
			require.Equal(t, tt.want, openAIStreamTerminalShouldFailover([]byte(tt.data), eventType, tt.clientOutputStarted))
		})
	}
}

var (
	benchmarkOpenAIResponseSSEEventTypeSink string
	benchmarkOpenAIResponseSSETerminalSink  bool
)

func BenchmarkOpenAIResponseSSETypeExtraction(b *testing.B) {
	data := `{"type":"response.output_text.delta","sequence_number":42,"delta":"streaming response benchmark payload"}`
	dataBytes := []byte(data)

	b.Run("legacy double parse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(dataBytes)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			benchmarkOpenAIResponseSSETerminalSink = openAIStreamEventIsTerminal(data)
			benchmarkOpenAIResponseSSEEventTypeSink = strings.TrimSpace(gjson.GetBytes(dataBytes, "type").String())
		}
	})

	b.Run("reused single parse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(dataBytes)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			eventTypeRaw := gjson.GetBytes(dataBytes, "type").String()
			benchmarkOpenAIResponseSSEEventTypeSink = strings.TrimSpace(eventTypeRaw)
			benchmarkOpenAIResponseSSETerminalSink = openAIStreamEventIsTerminalWithType(data, eventTypeRaw)
		}
	})
}
