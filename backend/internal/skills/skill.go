package skills

// Skill represents an agent skill discovered from a SKILL.md file.
type Skill struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Description string   `json:"description,omitempty"`
	Body        string   `json:"body,omitempty"`
	Source      string   `json:"source"`
	SkillFile   string   `json:"skillFile"`
}

// Preset describes a recommended skill available from the presets list.
type Preset struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description,omitempty"`
	DescriptionZh string   `json:"description_zh,omitempty"`
	Version       string   `json:"version,omitempty"`
	Author        string   `json:"author,omitempty"`
	URL           string   `json:"url,omitempty"`
	AgentTypes    []string `json:"agent_types,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Featured      bool     `json:"featured,omitempty"`
	Source        *Source  `json:"source,omitempty"`
	Pricing       *Pricing `json:"pricing,omitempty"`
}

// Source describes where a preset is hosted.
type Source struct {
	Provider string `json:"provider"`
	Name     string `json:"name,omitempty"`
	URL      string `json:"url,omitempty"`
}

// Pricing describes the pricing model for a preset.
type Pricing struct {
	Type     string  `json:"type"`
	Price    float64 `json:"price,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

// PresetsResponse is the top-level presets JSON schema.
type PresetsResponse struct {
	Version   int      `json:"version"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Skills    []Preset `json:"skills"`
}
