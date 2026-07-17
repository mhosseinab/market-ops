// Command dknormalize deterministically rewrites the FROZEN DK Seller OpenAPI
// document into a temporary, spec-valid document that oapi-codegen / kin-openapi
// will accept, WITHOUT editing the frozen source (docs/DK Marketplace - Open API
// Service.yml is read-only; it only changes on a deliberate re-freeze).
//
// The frozen doc carries informal, hand-authored schema-keyword violations that
// kin-openapi rejects:
//
//   - `type` set to non-OpenAPI tokens (bool, double, int, option, mixed, json,
//     datetime, "string,enum", null, ...). We canonicalize the unambiguous ones
//     (bool→boolean, double/real/float→number, int→integer) and DROP the `type`
//     keyword for anything ambiguous or compound — an untyped schema is valid and
//     generates a permissive Go type, which is correct for a client we only use
//     for a handful of endpoints. We never invent a semantic the source didn't state.
//   - `required` given as a boolean or a mapping inside a *schema* object (only an
//     array of property names is valid there), and as the string "true" on a
//     *parameter* (only a boolean is valid there). We coerce by structural context:
//     a mapping with both `in` and `name` is a Parameter (required must be bool);
//     otherwise it is a Schema (required must be an array — drop it if it is not).
//
// The rewrite is a pure YAML-node transform (order-preserving, deterministic), so
// regeneration is reproducible and the drift check stays meaningful.
//
// Usage: dknormalize <input.yml> <output.yml>
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: dknormalize <input.yml> <output.yml>")
		os.Exit(2)
	}
	in, out := os.Args[1], os.Args[2]

	raw, err := os.ReadFile(in)
	if err != nil {
		fail("read input: %v", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		fail("parse yaml: %v", err)
	}

	normalize(&doc)

	buf, err := yaml.Marshal(&doc)
	if err != nil {
		fail("marshal yaml: %v", err)
	}
	if err := os.WriteFile(out, buf, 0o644); err != nil {
		fail("write output: %v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dknormalize: "+format+"\n", args...)
	os.Exit(1)
}

// validType is the set of OpenAPI 3.0 primitive type tokens.
var validType = map[string]bool{
	"string": true, "number": true, "integer": true,
	"boolean": true, "array": true, "object": true,
}

// typeCanon maps the unambiguous informal tokens to their OpenAPI equivalent.
// Tokens not in validType and not in typeCanon cause the `type` keyword to be
// dropped (the schema becomes untyped, which is valid).
var typeCanon = map[string]string{
	"bool":   "boolean",
	"double": "number",
	"real":   "number",
	"float":  "number",
	"int":    "integer",
}

// normalize walks every node in the document applying the fixes described above.
func normalize(n *yaml.Node) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			normalize(c)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			normalize(c)
		}
	case yaml.MappingNode:
		normalizeMapping(n)
	}
}

// normalizeMapping rewrites a mapping node in place. Content is [k0,v0,k1,v1,...].
func normalizeMapping(m *yaml.Node) {
	isParam := mappingHasKeys(m, "in", "name")

	kept := make([]*yaml.Node, 0, len(m.Content))
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i]
		val := m.Content[i+1]

		switch key.Value {
		case "type":
			if drop := fixType(val); drop {
				continue // omit this key/value pair entirely
			}
		case "required":
			if drop := fixRequired(val, isParam); drop {
				continue
			}
		case "properties":
			fixProperties(val)
		case "oneOf", "anyOf", "allOf":
			fixComposition(val)
		case "enum":
			if drop := fixEnum(val); drop {
				continue
			}
		case "security":
			fixSecurity(val)
		case "tags":
			fixTags(val)
		case "paths":
			fixPaths(val)
		case "additionalProperties":
			fixAdditionalProperties(val)
		}

		// Recurse into the (possibly rewritten) value.
		normalize(val)
		kept = append(kept, key, val)
	}
	m.Content = kept
	reconcileEnumType(m)
}

// reconcileEnumType makes a schema carrying a scalar `enum` have a matching
// scalar `type`. The frozen doc sometimes pairs an enum of string/integer values
// with `type: array` (or no type at all); oapi-codegen then emits an enum whose
// underlying Go type is a slice and whose constants are unquoted — invalid code.
// Force `integer` when every member is an integer literal, otherwise `string`.
func reconcileEnumType(m *yaml.Node) {
	enum := mapValue(m, "enum")
	if enum == nil || enum.Kind != yaml.SequenceNode || len(enum.Content) == 0 {
		return
	}
	want := "integer"
	for _, e := range enum.Content {
		if e.Kind != yaml.ScalarNode {
			return // non-scalar members are handled (dropped) elsewhere
		}
		if !isIntegerLiteral(e.Value) {
			want = "string"
		}
	}
	if t := mapValue(m, "type"); t != nil {
		if t.Value == want && t.Kind == yaml.ScalarNode {
			return
		}
		*t = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: want}
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "type"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: want})
}

// isIntegerLiteral reports whether s is a base-10 integer (optionally signed).
func isIntegerLiteral(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i = 1
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// fixType canonicalizes or signals removal of a `type` value node.
// Returns true when the `type` keyword should be dropped.
func fixType(val *yaml.Node) bool {
	if val.Kind != yaml.ScalarNode {
		return true // a non-scalar type is meaningless; drop it
	}
	if val.Tag == "!!null" || val.Value == "" {
		return true // `type: null` (invalid in OpenAPI 3.0)
	}
	if validType[val.Value] {
		return false
	}
	if canon, ok := typeCanon[val.Value]; ok {
		val.Value = canon
		val.Tag = "!!str"
		val.Style = 0
		return false
	}
	return true // ambiguous/compound/informal token — drop the constraint
}

// fixRequired coerces a `required` value node by structural context.
// Returns true when the `required` keyword should be dropped.
func fixRequired(val *yaml.Node, isParam bool) bool {
	if isParam {
		// Parameter.required must be a boolean.
		if val.Kind == yaml.ScalarNode {
			switch val.Value {
			case "true", "false":
				val.Tag = "!!bool"
				val.Style = 0
				return false
			}
			if val.Tag == "!!bool" {
				return false
			}
		}
		// Anything non-boolean on a parameter: default to not-required.
		val.Kind = yaml.ScalarNode
		val.Tag = "!!bool"
		val.Value = "false"
		val.Content = nil
		val.Style = 0
		return false
	}
	// Schema.required must be an array of property names; drop anything else.
	return val.Kind != yaml.SequenceNode
}

// fixProperties repairs a `properties` value node. Two frozen-doc hazards:
//
//   - A property value that is a bare scalar (a stray type token or a description
//     string) rather than a Schema; replace it with an empty schema `{}`.
//   - A property KEY that carries no ASCII identifier character (e.g. a pure
//     Persian brand name used to illustrate a map-shaped object). oapi-codegen
//     sanitizes such a key to an EMPTY Go field name and emits invalid code;
//     drop those entries. The enclosing object stays valid (open/empty struct).
func fixProperties(props *yaml.Node) {
	if props.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(props.Content))
	for i := 0; i+1 < len(props.Content); i += 2 {
		pk := props.Content[i]
		pv := props.Content[i+1]
		if !hasASCIIIdentChar(pk.Value) {
			continue // un-nameable property key; drop the entry
		}
		if pv.Kind == yaml.ScalarNode {
			*pv = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		kept = append(kept, pk, pv)
	}
	props.Content = kept
}

// hasASCIIIdentChar reports whether s contains at least one ASCII letter or
// digit — the minimum for oapi-codegen to derive a non-empty Go identifier.
func hasASCIIIdentChar(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

// fixComposition repairs a oneOf/anyOf/allOf node. These keywords must hold a
// sequence of schemas; the frozen doc sometimes gives a single mapping. Wrap a
// stray mapping in a one-element sequence so the constraint stays valid.
func fixComposition(comp *yaml.Node) {
	if comp.Kind == yaml.MappingNode {
		inner := *comp
		*comp = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&inner}}
	}
}

// fixEnum repairs an `enum` node, which must be a sequence of scalars for a
// usable client. The frozen doc sometimes gives a single comma-joined string
// (wrap it in a one-element sequence rather than splitting, which would invent
// members) and sometimes gives a list of OBJECTS (informal documentation of
// example shapes, not a real constraint). Returns true when the enum should be
// dropped — i.e. it contains any non-scalar member, which oapi-codegen cannot
// render as Go constants.
func fixEnum(enum *yaml.Node) bool {
	if enum.Kind == yaml.ScalarNode {
		inner := *enum
		*enum = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&inner}}
		return false
	}
	if enum.Kind == yaml.SequenceNode {
		for _, e := range enum.Content {
			if e.Kind != yaml.ScalarNode {
				return true
			}
		}
	}
	return false
}

// fixSecurity repairs a `security` requirements list. Each requirement maps a
// scheme name to its scope list; the scope value must be a sequence. The frozen
// doc writes an empty mapping (`{ bearerAuth: {} }`) where an empty array is
// required — coerce any mapping scope value to an empty sequence.
func fixSecurity(sec *yaml.Node) {
	if sec.Kind != yaml.SequenceNode {
		return
	}
	for _, req := range sec.Content {
		if req.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(req.Content); i += 2 {
			scopes := req.Content[i+1]
			if scopes.Kind == yaml.MappingNode {
				*scopes = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			}
		}
	}
}

// fixTags repairs the top-level `tags` node. OpenAPI requires an array of Tag
// objects, but the frozen doc writes a mapping of tagName -> { name, count, ... }.
// Convert that mapping into a sequence of its value objects, keeping only the
// `name`/`description` fields kin-openapi understands. Operation-level `tags`
// are already sequences and are left untouched.
func fixTags(tags *yaml.Node) {
	if tags.Kind != yaml.MappingNode {
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := 0; i+1 < len(tags.Content); i += 2 {
		obj := tags.Content[i+1]
		if obj.Kind != yaml.MappingNode {
			continue
		}
		kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for j := 0; j+1 < len(obj.Content); j += 2 {
			k := obj.Content[j].Value
			if k == "name" || k == "description" {
				kept.Content = append(kept.Content, obj.Content[j], obj.Content[j+1])
			}
		}
		seq.Content = append(seq.Content, kept)
	}
	*tags = *seq
}

// fixAdditionalProperties collapses an inline `additionalProperties` *schema*
// (a mapping) to the boolean `true`. The frozen doc combines `properties` with
// an inline-object `additionalProperties`, which oapi-codegen renders as an
// invalid anonymous embedded struct. `true` keeps the "open object" semantics
// and generates a permissive `map[string]interface{}` instead.
func fixAdditionalProperties(ap *yaml.Node) {
	if ap.Kind == yaml.MappingNode {
		*ap = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// fixPaths ensures every `{placeholder}` in a path template has a declared path
// parameter. oapi-codegen rejects a path with positional parameters the spec does
// not declare. The frozen doc omits some; inject a minimal `in: path` string
// parameter at the path-item level (which kin-openapi merges into every operation)
// for any placeholder not already declared at the path-item or operation level.
func fixPaths(paths *yaml.Node) {
	if paths.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		tmpl := paths.Content[i].Value
		item := paths.Content[i+1]
		if item.Kind != yaml.MappingNode {
			continue
		}
		placeholders := pathPlaceholders(tmpl)
		if len(placeholders) == 0 {
			continue
		}
		declared := declaredPathParams(item)
		var missing []string
		for _, name := range placeholders {
			if !declared[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		params := itemParameters(item)
		for _, name := range missing {
			params.Content = append(params.Content, pathParamNode(name))
		}
	}
}

// pathPlaceholders returns the `{...}` placeholder names in a path template, in
// order, de-duplicated.
func pathPlaceholders(tmpl string) []string {
	var out []string
	seen := map[string]bool{}
	for {
		start := indexByte(tmpl, '{')
		if start < 0 {
			break
		}
		end := indexByte(tmpl[start:], '}')
		if end < 0 {
			break
		}
		name := tmpl[start+1 : start+end]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		tmpl = tmpl[start+end+1:]
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// declaredPathParams collects the names of in:path parameters declared at the
// path-item level and within each operation.
func declaredPathParams(item *yaml.Node) map[string]bool {
	out := map[string]bool{}
	collect := func(params *yaml.Node) {
		if params == nil || params.Kind != yaml.SequenceNode {
			return
		}
		for _, p := range params.Content {
			if p.Kind != yaml.MappingNode {
				continue
			}
			var in, name string
			for j := 0; j+1 < len(p.Content); j += 2 {
				switch p.Content[j].Value {
				case "in":
					in = p.Content[j+1].Value
				case "name":
					name = p.Content[j+1].Value
				}
			}
			if in == "path" && name != "" {
				out[name] = true
			}
		}
	}
	collect(mapValue(item, "parameters"))
	for j := 0; j+1 < len(item.Content); j += 2 {
		if httpMethods[item.Content[j].Value] {
			collect(mapValue(item.Content[j+1], "parameters"))
		}
	}
	return out
}

// itemParameters returns the path-item-level `parameters` sequence, creating it
// if absent.
func itemParameters(item *yaml.Node) *yaml.Node {
	if p := mapValue(item, "parameters"); p != nil {
		if p.Kind != yaml.SequenceNode {
			*p = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		}
		return p
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	item.Content = append(item.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "parameters"}, seq)
	return seq
}

// pathParamNode builds a minimal required in:path string parameter.
func pathParamNode(name string) *yaml.Node {
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	schema := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalar("type"), scalar("string"),
	}}
	req := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalar("in"), scalar("path"),
		scalar("name"), scalar(name),
		scalar("required"), req,
		scalar("schema"), schema,
	}}
}

// mapValue returns the value node for key within a mapping, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingHasKeys reports whether the mapping node has all the given scalar keys.
func mappingHasKeys(m *yaml.Node, keys ...string) bool {
	for _, want := range keys {
		found := false
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
