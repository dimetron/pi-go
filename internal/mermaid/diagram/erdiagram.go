package diagram

import (
	"regexp"
	"strings"
)

// erAttribute represents an attribute of an entity.
type erAttribute struct {
	attrType string
	name     string
	keys     []string // e.g. "PK", "FK", "UK"
	comment  string
}

// erEntity represents an entity in the ER diagram.
type erEntity struct {
	name       string
	alias      string
	attributes []erAttribute
}

// erRelationship represents a relationship between two entities.
type erRelationship struct {
	entity1   string
	entity2   string
	card1     string // cardinality on entity1 side: "||", "|o", "}|", "}o"
	card2     string // cardinality on entity2 side: "||", "o|", "|{", "o{"
	lineStyle string // "--" or ".."
	label     string
}

// erDiagram represents a parsed ER diagram.
type erDiagram struct {
	entities      map[string]*erEntity
	entityOrder   []string
	relationships []erRelationship
	direction     string // "TB" or "LR"
}

// cardinalityDisplay maps cardinality markers to display text.
var cardinalityDisplay = map[string]string{
	"||": "1",
	"|o": "0..1",
	"o|": "0..1",
	"}|": "1..*",
	"|{": "1..*",
	"}o": "0..*",
	"o{": "0..*",
}

// Regex patterns for ER diagram parsing.
var (
	reERDiagramHeader = regexp.MustCompile(`(?i)^\s*erDiagram\s*$`)
	reERDirection     = regexp.MustCompile(`(?i)^\s*direction\s+(LR|RL|TB|BT|TD)\s*$`)
	reEREntityOpen    = regexp.MustCompile(`^\s*(\w+)\s*\{\s*$`)
	reEREntityClose   = regexp.MustCompile(`^\s*\}\s*$`)
	reERAttribute     = regexp.MustCompile(`^\s*(\w+)\s+(\w+)\s*(?:((?:PK|FK|UK)(?:\s*,\s*(?:PK|FK|UK))*))?\s*(?:"([^"]*)")?\s*$`)
	reERRelationship  = regexp.MustCompile(
		`^\s*(\w+)\s+` +
			`(\|\||[|o}\s]{0,1}\||\}[|o]|\|o)` +
			`(--|\.\.)\s*` +
			`(\|\||\|[{o]|o[|{]|o\|)` +
			`\s+(\w+)` +
			`(?:\s*:\s*"?([^"]*)"?)?\s*$`,
	)
	reERRelationshipWords = regexp.MustCompile(
		`(?i)^\s*(\w+)\s+` +
			`(zero or one|one or more|zero or more|only one|one to one|one to many|many to one|many to many)` +
			`\s+(?:to\s+)?` +
			`(zero or one|one or more|zero or more|only one|one to one|one to many|many to one|many to many)?\s*` +
			`(\w+)` +
			`(?:\s*:\s*"?([^"]*)"?)?\s*$`,
	)
	reEREntityAlias = regexp.MustCompile(`^\s*(\w+)\s+"([^"]+)"\s*$`)
)

// wordToCardinality maps word-based cardinality to marker format.
var wordToCardinality = map[string]string{
	"zero or one":  "|o",
	"only one":     "||",
	"one or more":  "}|",
	"zero or more": "}o",
}

// erBoxInfo holds computed layout info for an entity box.
type erBoxInfo struct {
	name   string
	x, y   int
	width  int
	height int
	layer  int
	col    int
}

// parseERDiagram parses ER diagram source into an erDiagram model.
func parseERDiagram(source string) *erDiagram {
	erd := &erDiagram{
		entities:  make(map[string]*erEntity),
		direction: "TB",
	}

	lines := strings.Split(source, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])

		if trimmed == "" || strings.HasPrefix(trimmed, "%%") || reERDiagramHeader.MatchString(trimmed) {
			i++
			continue
		}

		if m := reERDirection.FindStringSubmatch(trimmed); m != nil {
			dir := strings.ToUpper(m[1])
			if dir == "TD" {
				dir = "TB"
			}
			erd.direction = dir
			i++
			continue
		}

		if m := reEREntityOpen.FindStringSubmatch(trimmed); m != nil {
			entityName := m[1]
			entity := erd.ensureEntity(entityName)
			i++
			for i < len(lines) {
				attrLine := strings.TrimSpace(lines[i])
				if reEREntityClose.MatchString(attrLine) {
					i++
					break
				}
				if attrLine == "" {
					i++
					continue
				}
				attr := parseERAttribute(attrLine)
				if attr != nil {
					entity.attributes = append(entity.attributes, *attr)
				}
				i++
			}
			continue
		}

		if m := reEREntityAlias.FindStringSubmatch(trimmed); m != nil {
			entity := erd.ensureEntity(m[1])
			entity.alias = m[2]
			i++
			continue
		}

		if m := reERRelationship.FindStringSubmatch(trimmed); m != nil {
			rel := erRelationship{
				entity1:   m[1],
				card1:     m[2],
				lineStyle: m[3],
				card2:     m[4],
				entity2:   m[5],
			}
			if len(m) > 6 {
				rel.label = strings.TrimSpace(m[6])
			}
			erd.ensureEntity(rel.entity1)
			erd.ensureEntity(rel.entity2)
			erd.relationships = append(erd.relationships, rel)
			i++
			continue
		}

		if m := reERRelationshipWords.FindStringSubmatch(trimmed); m != nil {
			card1Str := strings.ToLower(m[2])
			card2Str := strings.ToLower(m[3])
			if card2Str == "" {
				card2Str = card1Str
			}
			c1 := wordToCardinality[card1Str]
			c2 := wordToCardinality[card2Str]
			if c1 == "" {
				c1 = "||"
			}
			if c2 == "" {
				c2 = "||"
			}
			rel := erRelationship{
				entity1:   m[1],
				card1:     c1,
				lineStyle: "--",
				card2:     c2,
				entity2:   m[4],
			}
			if len(m) > 5 {
				rel.label = strings.TrimSpace(m[5])
			}
			erd.ensureEntity(rel.entity1)
			erd.ensureEntity(rel.entity2)
			erd.relationships = append(erd.relationships, rel)
			i++
			continue
		}

		i++
	}

	return erd
}

// ensureEntity creates an entity entry if it doesn't exist and returns it.
func (erd *erDiagram) ensureEntity(name string) *erEntity {
	if e, ok := erd.entities[name]; ok {
		return e
	}
	e := &erEntity{name: name}
	erd.entities[name] = e
	erd.entityOrder = append(erd.entityOrder, name)
	return e
}

// parseERAttribute parses a single entity attribute line.
func parseERAttribute(text string) *erAttribute {
	if m := reERAttribute.FindStringSubmatch(text); m != nil {
		attr := &erAttribute{
			attrType: m[1],
			name:     m[2],
		}
		if m[3] != "" {
			keyStr := strings.ReplaceAll(m[3], " ", "")
			attr.keys = strings.Split(keyStr, ",")
		}
		if m[4] != "" {
			attr.comment = m[4]
		}
		return attr
	}
	return nil
}

// formatERAttribute formats an attribute for display.
func formatERAttribute(attr erAttribute) string {
	var b strings.Builder
	b.WriteString(attr.attrType)
	b.WriteString(" ")
	b.WriteString(attr.name)
	if len(attr.keys) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(attr.keys, ","))
	}
	if attr.comment != "" {
		b.WriteString(" \"")
		b.WriteString(attr.comment)
		b.WriteString("\"")
	}
	return b.String()
}
