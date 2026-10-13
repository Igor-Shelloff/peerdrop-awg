package transfer

type Header struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Size      int64  `json:"size,omitempty"`
	Content   string `json:"content,omitempty"`
	AllowSave bool   `json:"allow_save,omitempty"`
}

type Response struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Path   string `json:"path,omitempty"`
}