package structs

type DeploymentDefinition struct {
	DCDefs []struct {
		Name  string `json:"name"`
		Specs struct {
			Mem   int `json:"mem"`
			CPU   int `json:"cpu"`
			Ports int `json:"ports"`
			Net   int `json:"net"`
		} `json:"specs"`
		GcmIP    string   		`json:"gcmIP"`
		AgentIPs []string 		`json:"agentIPs"`
		DeploymentPath string 	`json:"deploymentPath"`
		Namespace string		`json:"namespace"`
	} `json:"DC-def"`
}