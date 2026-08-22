package typing

import (
	"encoding/json"
	"fmt"
)

// Instance is a concrete resource that can be applied.
type Instance interface {
	Reference
	Apply() error
}

// InstanceList is a slice of Instance with JSON support.
type InstanceList []Instance

func (rl *InstanceList) UnmarshalJSON(b []byte) error {
	var result InstanceList
	err := json.Unmarshal(b, &result)
	if err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}
	*rl = result
	return nil
}

func (rl *InstanceList) MarshalJSON() ([]byte, error) {
	var err error
	tmp := make([]*SimpleReference, len(*rl))

	for i, r := range *rl {
		tmp[i] = &SimpleReference{Id: r.GetId(), Key: r.GetKey()}
		if err != nil {
			return nil, fmt.Errorf("serialization failed: %w", err)
		}
	}

	data, err := json.Marshal(tmp)
	if err != nil {
		return nil, fmt.Errorf("marshal instance list: %w", err)
	}
	return data, nil
}

func (rl *InstanceList) Unique() {
	check := make(map[string]bool)
	tmp := make(InstanceList, 0, len(*rl))

	for _, r := range *rl {
		_, exists := check[r.GetId()]
		if exists {
			continue
		}
		check[r.GetId()] = true
		tmp = append(tmp, r)

	}
	*rl = tmp
}
