package submissions

import (
	"encoding/json"
	"fmt"

	"github.com/intelimek/megamoot/apps/api/internal/compliance"
)

func marshalReport(r *compliance.Report) ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode compliance report: %w", err)
	}
	return raw, nil
}

// unmarshalReport reads a stored report. An empty object means the stage had
// no rule set configured, which is different from a report with no findings.
func unmarshalReport(raw []byte) (*compliance.Report, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return nil, nil
	}
	var r compliance.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode compliance report: %w", err)
	}
	return &r, nil
}
