// Package taxonomy fixes the vocabulary of the content tree: interview
// types, debugging flavors, take-home classes, disciplines, and level bands.
// Values live in code, not config, because each one implies machinery
// elsewhere (providers, harnesses, rubrics); adding one is a deliberate act.
package taxonomy

import (
	"regexp"
	"slices"
)

// Type is an interview type. Each type has its own engine and content shape.
type Type string

const (
	Debugging Type = "debugging" // live session in a disposable environment
	TakeHome  Type = "takehome"  // offline coding challenge plus live review
	SysDesign Type = "sysdesign" // offline design document plus live review
)

// Types lists every interview type in display order.
var Types = []Type{Debugging, TakeHome, SysDesign}

// Flavor is the technology substrate of a debugging scenario.
type Flavor string

const (
	Kubernetes   Flavor = "kubernetes"
	ComposeLinux Flavor = "compose-linux"
)

// Flavors lists every debugging flavor in display order.
var Flavors = []Flavor{Kubernetes, ComposeLinux}

// Class is the problem class of a take-home challenge.
type Class string

const (
	ConstrainedSystems    Class = "constrained-systems"
	LegacyRescue          Class = "legacy-rescue"
	UnderspecifiedProduct Class = "underspecified-product"
	OptimizationLadder    Class = "optimization-ladder"
)

// Classes lists every take-home class in display order.
var Classes = []Class{ConstrainedSystems, LegacyRescue, UnderspecifiedProduct, OptimizationLadder}

// Discipline is the domain a problem draws its texture from. Problems are
// graded on resourcefulness, never on prior knowledge of the discipline.
type Discipline string

const (
	Systems Discipline = "systems"
	Infra   Discipline = "infra"
	DataEng Discipline = "data-eng"
	AIML    Discipline = "ai-ml"
	CompBio Discipline = "comp-bio"
	Physics Discipline = "physics"
	Stats   Discipline = "stats"
	Signals Discipline = "signals"
)

// Disciplines lists every discipline in display order.
var Disciplines = []Discipline{Systems, Infra, DataEng, AIML, CompBio, Physics, Stats, Signals}

// Level is a calibration band. A problem declares which bands it can grade.
type Level string

const (
	Entry     Level = "entry"
	Mid       Level = "mid"
	Senior    Level = "senior"
	Staff     Level = "staff"
	Principal Level = "principal"
)

// Levels lists every level band in ascending order.
var Levels = []Level{Entry, Mid, Senior, Staff, Principal}

// ValidType reports whether v names a known interview type.
func ValidType(v Type) bool { return slices.Contains(Types, v) }

// ValidFlavor reports whether v names a known debugging flavor.
func ValidFlavor(v Flavor) bool { return slices.Contains(Flavors, v) }

// ValidClass reports whether v names a known take-home class.
func ValidClass(v Class) bool { return slices.Contains(Classes, v) }

// ValidDiscipline reports whether v names a known discipline.
func ValidDiscipline(v Discipline) bool { return slices.Contains(Disciplines, v) }

// ValidLevel reports whether v names a known level band.
func ValidLevel(v Level) bool { return slices.Contains(Levels, v) }

// idRe is the shape of every id in the content tree: problem ids, fault
// ids, tension and curveball ids.
var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidID reports whether s is a kebab-case id.
func ValidID(s string) bool { return idRe.MatchString(s) }
