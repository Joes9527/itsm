package intake

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/ent"
)

type WorkItemReference struct {
	WorkItemID int    `json:"workItemId"`
	Number     string `json:"number"`
	Status     string `json:"status"`
	URL        string `json:"url"`
}

type WorkItemReferencePage struct {
	Items      []WorkItemReference `json:"items"`
	NextCursor *string             `json:"nextCursor"`
}

// RequesterLifecycleReader dispatches through the existing WorkItem policy's
// professional resource. Owners are supplied by composition, so authorization
// and Intake do not depend on concrete domain services or their state machines.
type RequesterLifecycleReader struct {
	owners map[string]authorization.WorkItemLifecycleReader
}

func NewRequesterLifecycleReader(owners map[string]authorization.WorkItemLifecycleReader) *RequesterLifecycleReader {
	registered := make(map[string]authorization.WorkItemLifecycleReader, len(owners))
	for resource, owner := range owners {
		registered[resource] = owner
	}
	return &RequesterLifecycleReader{owners: registered}
}

func (r *RequesterLifecycleReader) IsUnfinished(ctx context.Context, client *ent.Client, item *ent.Ticket) (bool, error) {
	if r == nil || item == nil {
		return false, fmt.Errorf("requester lifecycle reader and WorkItem are required")
	}
	policy, err := authorization.ResolveWorkItemPolicy(item.RecordClass)
	if err != nil {
		return false, err
	}
	owner := r.owners[policy.Resource]
	if missingDependency(owner) {
		return false, fmt.Errorf("WorkItem lifecycle owner %q is unavailable", policy.Resource)
	}
	return owner.IsUnfinished(ctx, client, item)
}

// ticketReferenceURL receives config.Server.FrontendURL from composition, never
// the request Host. FrontendURL must be an HTTP(S) origin, optionally ending /.
func ticketReferenceURL(origin string, id int) (string, error) {
	base, err := url.Parse(origin)
	if err != nil || id <= 0 {
		return "", fmt.Errorf("invalid frontend origin or WorkItem identity")
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.ForceQuery || strings.Contains(origin, "#") || (base.Path != "" && base.Path != "/") {
		return "", fmt.Errorf("frontend URL must be an HTTP(S) origin without credentials, query or fragment")
	}
	base.Path = "/tickets/" + strconv.Itoa(id)
	base.RawPath = ""
	return base.String(), nil
}

func projectWorkItemReference(origin string, item *ent.Ticket) (WorkItemReference, error) {
	if item == nil {
		return WorkItemReference{}, fmt.Errorf("WorkItem is required for reference projection")
	}
	link, err := ticketReferenceURL(origin, item.ID)
	if err != nil {
		return WorkItemReference{}, err
	}
	return WorkItemReference{WorkItemID: item.ID, Number: item.TicketNumber, Status: item.Status, URL: link}, nil
}
