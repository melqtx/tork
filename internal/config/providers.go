package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuiltinSource keeps config IDs, display labels and defaults in one catalog.
// RuntimeName preserves the names used by existing results and health history.
type BuiltinSource struct {
	ID          string
	Label       string
	RuntimeName string
	Config      ProviderConfig
}

func BuiltinSources() []BuiltinSource {
	return []BuiltinSource{
		{"knaben", "Knaben", "knaben", ProviderConfig{Enabled: true, Type: "knaben", Mirror: "https://knaben.org"}},
		{"yts", "YTS", "yts", ProviderConfig{Enabled: true, Type: "yts", Mirror: "https://yts.mx"}},
		{"nyaa", "Nyaa", "nyaa", ProviderConfig{Enabled: true, Type: "nyaa", Mirror: "https://nyaa.si"}},
		// Knaben already aggregates TPB; additional sources remain opt-in.
		{"tpb_movies", "Pirate Bay (movies)", "tpb-movies", ProviderConfig{Type: "tpb_movies", Mirror: "https://apibay.org"}},
		{"tpb_tv", "Pirate Bay (TV)", "tpb-tv", ProviderConfig{Type: "tpb_tv", Mirror: "https://apibay.org"}},
		{"eztv", "EZTV", "eztv", ProviderConfig{Type: "eztv_html", Mirror: "https://eztvx.to"}},
		{"x1337", "1337x", "1337x", ProviderConfig{Type: "1337x_html", Mirrors: []string{"https://1337x.to", "https://1337x.st", "https://x1337x.ws", "https://x1337x.eu", "https://x1337x.cc"}}},
		{"tpb", "The Pirate Bay", "tpb", ProviderConfig{Type: "tpb", Mirror: "https://apibay.org"}},
		{"therarbg", "TheRARBG", "therarbg", ProviderConfig{Type: "therarbg", Mirror: "https://therarbg.to"}},
		{"limetorrents", "LimeTorrents", "limetorrents", ProviderConfig{Type: "limetorrents", Mirror: "https://www.limetorrents.fun"}},
	}
}

func defaultProviders() map[string]ProviderConfig {
	out := make(map[string]ProviderConfig)
	for _, s := range BuiltinSources() {
		out[s.ID] = s.Config
	}
	return out
}

// WriteProviderEnabled edits only one enabled flag, retaining unrelated YAML,
// comments and file permissions. The caller updates its in-memory config only
// after this succeeds. It never repairs malformed config or follows symlinks.
func (c *Config) WriteProviderEnabled(id string, enabled bool) error {
	if _, ok := c.Providers[id]; !ok {
		return errors.New("unknown source")
	}
	doc, check, mode, err := c.editableSourcesDocument()
	if err != nil {
		return err
	}
	if _, ok := check.Providers[id]; !ok {
		return errors.New("source was removed from config; restart before changing it")
	}
	root := doc.Content[0]
	providers, err := editableMapping(root, "providers")
	if err != nil {
		return err
	}
	source, err := editableMapping(providers, id)
	if err != nil {
		return err
	}
	index := mappingKey(source, "enabled")
	value := strconv.FormatBool(enabled)
	if index >= 0 {
		node := source.Content[index+1]
		if node.Kind != yaml.ScalarNode || node.Anchor != "" {
			return errors.New("source enabled flag must be a scalar without a YAML anchor")
		}
		node.Kind, node.Tag, node.Value, node.Style = yaml.ScalarNode, "!!bool", value, 0
	} else {
		source.Content = append(source.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value})
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return writeYAMLAtomic(c.ConfigPath(), doc, mode)
}

func editableMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	if i := mappingKey(parent, key); i >= 0 {
		node := parent.Content[i+1]
		if node.Anchor != "" {
			return nil, errors.New("expand YAML anchors before changing sources")
		}
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("config %s must be a YAML mapping", key)
		}
		return node, nil
	}
	// Editing an inherited mapping would replace its other keys on reload.
	if mappingKey(parent, "<<") >= 0 {
		return nil, errors.New("expand YAML merge keys before changing sources")
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
	return node, nil
}

// IsBuiltinSource reserves both config IDs and runtime names so custom sources
// cannot collide with builtin result identities.
func IsBuiltinSource(id string) bool {
	for _, s := range BuiltinSources() {
		if s.ID == id || s.RuntimeName == id {
			return true
		}
	}
	return false
}

func (c *Config) editableSourcesDocument() (*yaml.Node, *Config, os.FileMode, error) {
	data, mode, exists, err := readEditableConfig(c.ConfigPath())
	if err != nil {
		return nil, nil, 0, err
	}
	if !exists {
		data, err = yaml.Marshal(Default(c.dir))
		mode = 0o600
	}
	if err != nil {
		return nil, nil, 0, err
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, 0, errors.New("config is invalid YAML; fix it before changing sources")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, 0, errors.New("config must contain exactly one YAML document")
	}
	check := Default(c.dir)
	if err := doc.Decode(check); err != nil {
		return nil, nil, 0, errors.New("config is invalid; fix it before changing sources")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, 0, errors.New("config root must be a YAML mapping")
	}
	return &doc, check, mode, nil
}

// WriteCustomSource preserves unrelated source fields/comments. All custom-feed
// saves use owner-only permissions, including URLs with embedded credentials.
func (c *Config) WriteCustomSource(id string, p ProviderConfig, replace bool) error {
	if IsBuiltinSource(id) {
		return errors.New("built-in sources cannot be replaced")
	}
	if id == "" || len(id) > 64 {
		return errors.New("source name must produce an ID of 1–64 characters")
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return errors.New("source ID contains unsupported characters")
		}
	}
	if p.Type != "rss" && p.Type != "torznab" {
		return errors.New("custom sources must use RSS or Torznab")
	}
	doc, current, _, err := c.editableSourcesDocument()
	if err != nil {
		return err
	}
	_, exists := current.Providers[id]
	if exists != replace {
		if exists {
			return errors.New("a source with this name already exists")
		}
		return errors.New("source was removed from config; restart before editing it")
	}
	providers, err := editableMapping(doc.Content[0], "providers")
	if err != nil {
		return err
	}
	source, err := editableMapping(providers, id)
	if err != nil {
		return err
	}
	for _, field := range []struct{ key, value, tag string }{
		{"name", p.Label, "!!str"}, {"type", p.Type, "!!str"}, {"search_url", p.SearchURL, "!!str"}, {"api_key", p.APIKey, "!!str"}, {"enabled", strconv.FormatBool(p.Enabled), "!!bool"},
	} {
		i := mappingKey(source, field.key)
		if i >= 0 {
			node := source.Content[i+1]
			if node.Kind != yaml.ScalarNode || node.Anchor != "" {
				return errors.New("expand YAML aliases and anchors before editing source fields")
			}
			if field.key == "api_key" && field.value == "" {
				source.Content = append(source.Content[:i], source.Content[i+2:]...)
				continue
			}
			node.Tag, node.Value, node.Style = field.tag, field.value, 0
		} else if field.value != "" {
			source.Content = append(source.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: field.tag, Value: field.value})
		}
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	return writeYAMLAtomic(c.ConfigPath(), doc, 0o600)
}

func (c *Config) RemoveCustomSource(id string) error {
	if IsBuiltinSource(id) {
		return errors.New("built-in sources can be disabled, not removed")
	}
	doc, _, mode, err := c.editableSourcesDocument()
	if err != nil {
		return err
	}
	root := doc.Content[0]
	// Do not create a providers mapping while trying to remove a source.
	if mappingKey(root, "providers") < 0 {
		return errors.New("source no longer exists")
	}
	providers, err := editableMapping(root, "providers")
	if err != nil {
		return err
	}
	i := mappingKey(providers, id)
	if i < 0 {
		return errors.New("source no longer exists")
	}
	// Removing an anchor might strand aliases elsewhere in the document.
	if containsYAMLAnchor(providers.Content[i+1]) {
		return errors.New("expand YAML anchors before removing sources")
	}
	providers.Content = append(providers.Content[:i], providers.Content[i+2:]...)
	return writeYAMLAtomic(c.ConfigPath(), doc, mode)
}

func containsYAMLAnchor(node *yaml.Node) bool {
	if node.Anchor != "" {
		return true
	}
	for _, child := range node.Content {
		if containsYAMLAnchor(child) {
			return true
		}
	}
	return false
}
