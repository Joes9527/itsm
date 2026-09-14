package relationmetadata

type Endpoint struct {
	WorkItemID  int    `json:"workItemId"`
	Number      string `json:"number"`
	RecordClass string `json:"recordClass"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
}

type View struct {
	ID       int      `json:"id"`
	Type     string   `json:"relationType"`
	Required bool     `json:"required"`
	Source   Endpoint `json:"source"`
	Target   Endpoint `json:"target"`
}
