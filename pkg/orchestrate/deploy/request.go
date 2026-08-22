package deploy

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// RequestContext carries injected dependencies for request processing.
type RequestContext struct {
	Registry  *typing.Registry
	DB        *state.DB
	Providers *ProviderRegistry
}

// Request is a single unit of work in a deployment.
type Request interface {
	GetType() RequestType
	GetSubjectId() string
	GetSubjectKey() typing.DefinitionKey
	Process(ctx *RequestContext) error
	GetStatus(ctx *RequestContext) (typing.RolloutStatusShort, error)
	String() string
}

// RequestType classifies requests.
type RequestType string

const (
	RequestTypeCreate RequestType = "CREATE"
	RequestTypeDelete RequestType = "DELETE"
	RequestTypeStatus RequestType = "STATUS"
)

var _ Request = &CreateRequest{}

// CreateRequest applies a new resource instance.
type CreateRequest struct {
	SubjectId  string               `json:"subjectId"`
	Subject    json.RawMessage      `json:"subject"`
	SubjectKey typing.DefinitionKey `json:"subjectKey"`
}

func (cr *CreateRequest) String() string {
	return fmt.Sprintf("[create-request for %s %s, body length: %d]",
		cr.SubjectKey, cr.SubjectId, len(cr.Subject))
}

func (cr *CreateRequest) GetStatus(ctx *RequestContext) (typing.RolloutStatusShort, error) {
	rollout, err := LoadRollout(ctx.DB, cr.SubjectId)
	if err != nil {
		return typing.RolloutStatusError,
			fmt.Errorf("couldn't check rollout for %s: %w", cr, err)
	}
	if rollout == nil {
		return typing.RolloutStatusPending, nil
	}
	return rollout.GetStatus().GetShort(), nil
}

func (cr *CreateRequest) GetSubjectId() string {
	return cr.SubjectId
}

func (cr *CreateRequest) GetSubjectKey() typing.DefinitionKey {
	return cr.SubjectKey
}

func (cr *CreateRequest) GetType() RequestType {
	return RequestTypeCreate
}

func (cr *CreateRequest) Process(ctx *RequestContext) error {
	instance, err := ctx.Registry.LoadInstance(cr.SubjectKey, cr.Subject)
	if err != nil {
		return fmt.Errorf("couldn't load subject for %s: %w", cr, err)
	}

	rollout := Rollout{
		InstanceId:  cr.SubjectId,
		InstanceKey: cr.SubjectKey,
		Body:        cr.Subject,
	}

	applyErr := instance.Apply()
	if applyErr != nil {
		applyErr = fmt.Errorf("couldn't apply %s: %w", instance, applyErr)
		rollout.Status = &RolloutStatus{
			Short: typing.RolloutStatusError,
		}
		rollout.Errors = []string{applyErr.Error()}
	} else {
		rollout.Status = &RolloutStatus{
			Short: typing.RolloutStatusSuccess,
		}
	}

	err = ctx.DB.Save(state.RolloutsById, &rollout)
	if err != nil {
		log.Printf("deploy: WARNING: couldn't persist rollout %s: %s",
			rollout, err)
	}

	return applyErr
}

var _ Request = &DeleteRequest{}

// DeleteRequest removes a resource instance.
type DeleteRequest struct {
	SubjectId  string               `json:"subject_id"`
	SubjectKey typing.DefinitionKey `json:"subjectKey"`
}

func (dr *DeleteRequest) String() string {
	return fmt.Sprintf("[delete-request for key: '%s', id: '%s']",
		dr.SubjectId, dr.SubjectKey)
}

func (dr *DeleteRequest) GetSubjectId() string {
	return dr.SubjectId
}

func (dr *DeleteRequest) GetSubjectKey() typing.DefinitionKey {
	return dr.SubjectKey
}

func (dr *DeleteRequest) GetType() RequestType {
	return RequestTypeDelete
}

func (dr *DeleteRequest) Process(ctx *RequestContext) error {
	reference := typing.NewReference(dr.SubjectId, dr.SubjectKey)
	err := ctx.Registry.Delete(reference)
	if err != nil {
		return fmt.Errorf("deletion of %s %s failed: %w", dr.SubjectKey, dr.SubjectId, err)
	}
	r := typing.NewReference(dr.SubjectId, dr.SubjectKey)
	err = ctx.DB.RemoveLink(r)
	if err != nil {
		return fmt.Errorf("removing link for %s %s failed: %w", dr.SubjectKey, dr.SubjectId, err)
	}
	return nil
}

func (dr *DeleteRequest) GetStatus(ctx *RequestContext) (typing.RolloutStatusShort, error) {
	rollout, err := LoadRollout(ctx.DB, dr.SubjectId)
	if err != nil {
		return typing.RolloutStatusError,
			fmt.Errorf("couldn't check rollout for %s: %w", dr, err)
	}
	if rollout == nil {
		return typing.RolloutStatusSuccess, nil
	}
	return rollout.GetStatus().GetShort(), nil
}

var _ Request = &StatusRequest{}

// StatusRequest checks and updates the provider status of a resource.
type StatusRequest struct {
	SubjectId  string               `json:"subject_id"`
	SubjectKey typing.DefinitionKey `json:"subjectKey"`
}

func (sr *StatusRequest) String() string {
	return fmt.Sprintf("[status-request for key: '%s', id: '%s']",
		sr.SubjectId, sr.SubjectKey)
}

func (sr *StatusRequest) GetSubjectId() string {
	return sr.SubjectId
}

func (sr *StatusRequest) GetSubjectKey() typing.DefinitionKey {
	return sr.SubjectKey
}

func (sr *StatusRequest) GetType() RequestType {
	return RequestTypeStatus
}

func (sr *StatusRequest) Process(ctx *RequestContext) error {
	definition, err := ctx.Registry.GetDefinition(sr.SubjectKey)
	if err != nil {
		return fmt.Errorf("couldn't get definition %s: %w", sr.SubjectKey, err)
	}
	reference := typing.NewReference(sr.SubjectId, sr.SubjectKey)
	status, err := definition.GetProviderStatus(reference)
	if err != nil {
		return fmt.Errorf("couldn't get provider status for %s: %w", reference, err)
	}

	rollout, err := LoadRollout(ctx.DB, sr.SubjectId)
	if err != nil {
		log.Printf("couldn't check rollout for %s: %s", reference, err)
		return nil
	}

	rollout.UpdateStatus(status)
	err = ctx.DB.Save(state.RolloutsById, rollout)
	if err != nil {
		log.Printf("couldn't update rollout for %s: %s", reference, err)
	}

	return nil
}

func (sr *StatusRequest) GetStatus(_ *RequestContext) (typing.RolloutStatusShort, error) {
	return typing.RolloutStatusRunning, nil
}

// SerializedRequest is used for JSON persistence of polymorphic requests.
type SerializedRequest struct {
	Type RequestType     `json:"Type"`
	Body json.RawMessage `json:"Body"`
}

// RequestList is a slice of Request with JSON support.
type RequestList []Request

func (rl *RequestList) UnmarshalJSON(b []byte) error {
	var serializedRequests []*SerializedRequest
	err := json.Unmarshal(b, &serializedRequests)
	if err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}
	result := make(RequestList, len(serializedRequests))
	for i, serializedRequest := range serializedRequests {
		var request Request
		switch serializedRequest.Type {
		case RequestTypeDelete:
			request = &DeleteRequest{}
		case RequestTypeCreate:
			request = &CreateRequest{}
		case RequestTypeStatus:
			request = &StatusRequest{}
		default:
			panic("bad request type: " + serializedRequest.Type)
		}
		err = json.Unmarshal(serializedRequest.Body, request)
		if err != nil {
			return fmt.Errorf("unmarshal failed for %s: %w", serializedRequest.Type, err)
		}
		result[i] = request
	}

	*rl = result
	return nil
}

func (rl *RequestList) MarshalJSON() ([]byte, error) {
	var err error
	tmp := make([]SerializedRequest, len(*rl))

	for i, r := range *rl {
		entry := &tmp[i]
		entry.Type = r.GetType()
		entry.Body, err = json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("serialization failed for %s %s: %w", r.GetType(), r.GetSubjectId(), err)
		}
	}

	data, marshalErr := json.Marshal(tmp)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal request list: %w", marshalErr)
	}
	return data, nil
}
