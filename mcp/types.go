package mcp

type ClientCapabilities struct {
	Roots *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"roots,omitempty"`
	Sampling    *struct{} `json:"sampling,omitempty"`
	Elicitation *struct{} `json:"elicitation,omitempty"`
}

type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	Logging *struct{} `json:"logging,omitempty"`
	Prompts *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"prompts,omitempty"`
	Resources *struct {
		ListChanged bool `json:"listChanged"`
		Subscribe   bool `json:"subscribe"`
	} `json:"resources,omitempty"`
	Tools *struct {
		ListChanged bool `json:"listChanged"`
	} `json:"tools,omitempty"`
}

type ToolDescriptor struct {
}
