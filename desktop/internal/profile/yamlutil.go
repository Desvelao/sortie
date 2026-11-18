package profile

import (
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var errEOF = io.EOF

var pathPartRe = regexp.MustCompile(`^([A-Za-z_]+)(?:\[(\d+)\])?$`)

// lineOf finds the line of a path such as "tags[2]" or
// "destination_template" in a parsed document; 0 if it is not found. When the
// key exists but the item index does not, the line of the key is returned.
func lineOf(root *yaml.Node, path string) int {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	line := 0
	for _, part := range strings.Split(path, ".") {
		m := pathPartRe.FindStringSubmatch(part)
		if m == nil || n.Kind != yaml.MappingNode {
			return line
		}
		var val *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == m[1] {
				line, val = n.Content[i].Line, n.Content[i+1]
				break
			}
		}
		if val == nil {
			return line
		}
		if m[2] != "" {
			idx, _ := strconv.Atoi(m[2])
			if val.Kind != yaml.SequenceNode || idx >= len(val.Content) {
				return line
			}
			val = val.Content[idx]
			line = val.Line
		}
		n = val
	}
	return line
}

// cleanYAMLError trims yaml.v3's "yaml: " prefix and shows "line N: ..." for
// type errors, which carry their own line numbers.
func cleanYAMLError(err error) error {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		return errors.New(strings.Join(te.Errors, "; "))
	}
	return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
}

// carryComments copies the comments of the YAML in old onto the YAML in new
// (same document structure, different values) and returns the result. ok is
// false when either side cannot be parsed or nothing needed to be carried, in
// which case new is used unchanged.
func carryComments(old, new []byte) ([]byte, bool) {
	var o, n yaml.Node
	if yaml.Unmarshal(old, &o) != nil || yaml.Unmarshal(new, &n) != nil {
		return nil, false
	}
	if !copyComments(&o, &n) {
		return nil, false
	}
	out, err := marshalAny(&n)
	if err != nil {
		return nil, false
	}
	return out, true
}

// copyComments merges comments from o into n; reports whether any were copied.
func copyComments(o, n *yaml.Node) bool {
	copied := false
	cp := func(dst, src *string) {
		if *dst == "" && *src != "" {
			*dst, copied = *src, true
		}
	}
	cp(&n.HeadComment, &o.HeadComment)
	cp(&n.LineComment, &o.LineComment)
	cp(&n.FootComment, &o.FootComment)
	switch {
	case o.Kind == yaml.DocumentNode && n.Kind == yaml.DocumentNode:
		if len(o.Content) > 0 && len(n.Content) > 0 && copyComments(o.Content[0], n.Content[0]) {
			copied = true
		}
	case o.Kind == yaml.MappingNode && n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			for j := 0; j+1 < len(o.Content); j += 2 {
				if o.Content[j].Value != n.Content[i].Value {
					continue
				}
				if copyComments(o.Content[j], n.Content[i]) {
					copied = true
				}
				if copyComments(o.Content[j+1], n.Content[i+1]) {
					copied = true
				}
				break
			}
		}
	case o.Kind == yaml.SequenceNode && n.Kind == yaml.SequenceNode:
		for i, item := range n.Content {
			if j := matchItem(o, item, i); j >= 0 && copyComments(o.Content[j], item) {
				copied = true
			}
		}
	}
	return copied
}

// matchItem finds the item of old sequence o that corresponds to item (the
// i-th of the new sequence): the one with the same "name", else the same
// position.
func matchItem(o, item *yaml.Node, i int) int {
	if name := nameOf(item); name != "" {
		for j, x := range o.Content {
			if nameOf(x) == name {
				return j
			}
		}
		return -1
	}
	if i < len(o.Content) {
		return i
	}
	return -1
}

func nameOf(n *yaml.Node) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "name" {
			return n.Content[i+1].Value
		}
	}
	return ""
}
