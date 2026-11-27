package ctxerr

import (
	"context"
	"encoding/json"
)

type ErrorAgg struct {
	Count    int             `json:"count"`
	Loc      []string        `json:"loc"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// Aggregate retrieves all errors in the store and returns an aggregated,
// json-formatted summary containing:
// - The number of occurrences of each error
// - A reduced stack trace used for debugging the error
// - Additional metadata present for vital errors
func Aggregate(ctx context.Context) (json.RawMessage, error) {
	const maxTraceLen = 3
	empty := json.RawMessage("[]")

	storedErrs, err := Retrieve(ctx)
	if err != nil {
		return empty, Wrap(ctx, err, "retrieve on aggregation")
	}

	aggs := make([]ErrorAgg, len(storedErrs))
	for i, stored := range storedErrs {
		var ferr []automdmErrorJSON
		if err = json.Unmarshal(stored.Chain, &ferr); err != nil {
			return empty, Wrap(ctx, err, "unmarshal on aggregation")
		}

		stack := aggregateStack(ferr, maxTraceLen)
		meta := getVitalMetadata(ferr)
		aggs[i] = ErrorAgg{stored.Count, stack, meta}
	}

	return json.Marshal(aggs)
}

// aggregateStack creates a single stack trace by joining all the stack traces in
// an error chain
func aggregateStack(chain []automdmErrorJSON, maxStack int) []string {
	stack := make([]string, maxStack)
	stackIdx := 0

out:
	for _, e := range chain {
		for _, m := range e.Stack {
			if stackIdx >= maxStack {
				break out
			}

			stack[stackIdx] = m
			stackIdx++
		}
	}

	return stack[:stackIdx]
}

// vitalErrorData represents the structure of vital automdmd error data.
type vitalErrorData struct {
	ErrorSource         string         `json:"error_source"`
	ErrorSourceVersion  string         `json:"error_source_version"`
	ErrorMessage        string         `json:"error_message"`
	ErrorAdditionalInfo map[string]any `json:"error_additional_info"`
	Vital               bool           `json:"vital"`
}

func getVitalMetadata(chain []automdmErrorJSON) json.RawMessage {
	for _, e := range chain {
		if len(e.Data) > 0 {
			// Currently, only vital automdmd errors contain metadata.
			// Note: vital errors should not contain any sensitive info
			var automdmdErr vitalErrorData
			var err error
			if err = json.Unmarshal(e.Data, &automdmdErr); err != nil || !automdmdErr.Vital {
				continue
			}
			export := map[string]interface{}{
				"error_source":          automdmdErr.ErrorSource,
				"error_source_version":  automdmdErr.ErrorSourceVersion,
				"error_message":         automdmdErr.ErrorMessage,
				"error_additional_info": automdmdErr.ErrorAdditionalInfo,
			}
			var meta json.RawMessage
			if meta, err = json.Marshal(export); err != nil {
				return nil
			}
			return meta
		}
	}
	return nil
}
