package config

import (
	"bytes"
	"encoding/json"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"
)

// Comments are attached to a config when it is written out: Head above the
// document, and Items above entries of a top-level list, keyed by the list's
// field name and the entry's index ("workloads", 2).
type Comments struct {
	Head  []string
	Items map[string]map[int][]string
}

// AddItem attaches a comment line to entry i of the top-level list field.
func (c *Comments) AddItem(field string, i int, line string) {
	if c.Items == nil {
		c.Items = map[string]map[int][]string{}
	}
	if c.Items[field] == nil {
		c.Items[field] = map[int][]string{}
	}
	c.Items[field][i] = append(c.Items[field][i], line)
}

// EncodeYAML writes v as block-style YAML with two-space indents, in the
// field order of its Go type, with optional comments.
func EncodeYAML(v any, comments *Comments) ([]byte, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yamlv3.Node
	if err := yamlv3.Unmarshal(j, &doc); err != nil {
		return nil, err
	}
	plain(&doc)
	if comments != nil && len(doc.Content) == 1 {
		root := doc.Content[0]
		if len(comments.Head) > 0 {
			root.HeadComment = commentBlock(comments.Head)
		}
		for i := 0; i+1 < len(root.Content); i += 2 {
			key, val := root.Content[i], root.Content[i+1]
			items := comments.Items[key.Value]
			if val.Kind != yamlv3.SequenceNode || items == nil {
				continue
			}
			for idx, lines := range items {
				if idx < len(val.Content) {
					val.Content[idx].HeadComment = commentBlock(lines)
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := yamlv3.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// plain turns the flow style and quoting that JSON input produces into
// ordinary block YAML.
func plain(n *yamlv3.Node) {
	n.Style = 0
	for _, c := range n.Content {
		plain(c)
	}
}

func commentBlock(lines []string) string {
	var out []string
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			out = append(out, "# "+strings.TrimRight(part, " "))
		}
	}
	return strings.Join(out, "\n")
}
