package tokens

import "encoding/json"

// EstimateJSON estimates the token cost of a value once serialized to JSON.
//
// This matters for tool schemas: every request re-sends the full JSON Schema
// for every tool, which is a large fixed overhead that is invisible if you only
// measure the message list.
func EstimateJSON(v any) int {
	if v == nil {
		return 0
	}
	data, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return EstimateText(string(data))
}
