package plugin

const ManifestSchemaVersion = "1"

type Manifest struct {
	SchemaVersion string `json:"schema_version"`

	Name    string `json:"name"`
	Version string `json:"version"`

	Summary string `json:"summary"`

	RequiresLfed string `json:"requires_lfed"`
}
