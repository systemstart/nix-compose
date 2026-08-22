package composeimport

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// keyOrder is the order service keys are written in. Alphabetical output is
// deterministic but reads badly — what a service *is* (`image` or `package`)
// belongs at the top, not between `extra_hosts` and `labels`. Keys not listed
// here follow, alphabetically.
var keyOrder = []string{
	"image",
	"package",
	"entrypoint",
	"command",
	"ports",
	"environment",
	"volumes",
	"tmpfs",
	"depends_on",
	"healthcheck",
	"restart",
	"profiles",
}

// Render writes the converted document as YAML, with a header explaining where
// the file came from and what to do next.
func Render(res *Result, source string) ([]byte, error) {
	body, err := renderDoc(res)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Converted from %s by `nix-compose import`.\n", source)
	buf.WriteString("#\n")
	buf.WriteString("# Every service below still pulls its image from a registry, exactly as\n")
	buf.WriteString("# it did before. To stop doing that for a service, replace its `image:`\n")
	buf.WriteString("# with `package: <nixpkgs attribute>` — the image is then built from that\n")
	buf.WriteString("# package's closure and imported straight into the runtime. Run\n")
	buf.WriteString("# `nix-compose suggest` to see which images have a nixpkgs equivalent.\n")
	buf.WriteString("#\n")
	buf.WriteString("# The two kinds of service mix freely, so this can happen one at a time.\n")

	if len(res.Notes) > 0 {
		buf.WriteString("#\n")
		buf.WriteString("# Not everything survived the conversion:\n")
		for _, note := range res.Notes {
			buf.WriteString("#   - " + note.String() + "\n")
		}
	}

	buf.WriteString("\n")
	buf.Write(body)
	return buf.Bytes(), nil
}

func renderDoc(res *Result) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}

	needsImage := map[string]bool{}
	for _, name := range res.NeedsImage {
		needsImage[name] = true
	}

	services := &yaml.Node{Kind: yaml.MappingNode}
	raw, _ := res.Doc["services"].(map[string]any)
	for _, name := range res.Services {
		svc, _ := raw[name].(map[string]any)
		node, err := orderedMapping(svc)
		if err != nil {
			return nil, err
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: name}
		if needsImage[name] {
			// Put the hole where the reader is looking, not only in the
			// header: this service cannot start as written.
			key.HeadComment = "FIXME: nothing to run here — this service was built from a\n" +
				"Dockerfile. Add `package: <nixpkgs attribute>` or `image: <ref>`."
		}
		services.Content = append(services.Content, key, node)
	}
	appendPair(root, "services", services)

	for _, key := range []string{"networks", "volumes"} {
		value, ok := res.Doc[key]
		if !ok {
			continue
		}
		node := &yaml.Node{}
		if err := node.Encode(value); err != nil {
			return nil, fmt.Errorf("encoding %s: %w", key, err)
		}
		appendPair(root, key, node)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("encoding document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// orderedMapping encodes a service with the important keys first.
func orderedMapping(svc map[string]any) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}

	written := map[string]bool{}
	write := func(key string) error {
		value, ok := svc[key]
		if !ok || written[key] {
			return nil
		}
		written[key] = true

		// Ports are quoted, because a bare `8080:80` is only unambiguous
		// under a YAML 1.2 parser and compose files get read by more than
		// one. Under the 1.1 rules it is a sexagesimal integer.
		if child := quotedSequence(key, value); child != nil {
			appendPair(node, key, child)
			return nil
		}

		child := &yaml.Node{}
		if err := child.Encode(value); err != nil {
			return fmt.Errorf("encoding %s: %w", key, err)
		}
		appendPair(node, key, child)
		return nil
	}

	for _, key := range keyOrder {
		if err := write(key); err != nil {
			return nil, err
		}
	}

	rest := make([]string, 0, len(svc))
	for key := range svc {
		if !written[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		if err := write(key); err != nil {
			return nil, err
		}
	}

	return node, nil
}

// quotedSequence renders a list of strings with explicit quotes, or returns
// nil when the value is not a list this applies to.
func quotedSequence(key string, value any) *yaml.Node {
	if key != "ports" {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}

	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil // fall back to the default encoding
		}
		seq.Content = append(seq.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: s,
			Style: yaml.DoubleQuotedStyle,
		})
	}
	return seq
}

func appendPair(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		value,
	)
}

// NotesReport renders the conversion notes for the terminal.
func NotesReport(res *Result) string {
	if len(res.Notes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, note := range res.Notes {
		b.WriteString("  ! " + note.String() + "\n")
	}
	return b.String()
}
