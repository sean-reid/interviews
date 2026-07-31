package bundle

import "github.com/sean-reid/interviews/internal/taxonomy"

// spec is what delivery looks like for one interview type: the front page
// written at the bundle root, and whether the drop is a git repository.
type spec struct {
	about   string
	gitInit bool
}

// specs holds one entry per deliverable type. A take-home comes back as a
// repository, so it ships as one; a design exercise comes back as a
// document, so a repository would only be an empty ceremony around it.
var specs = map[taxonomy.Type]spec{
	taxonomy.TakeHome:  {about: takeHomeAbout, gitInit: true},
	taxonomy.SysDesign: {about: sysDesignAbout},
}

// takeHomeAbout orients the candidate without exposing anything about how
// the problem is parameterized or graded.
const takeHomeAbout = `# {{.Title}}

Start with candidate/brief.md; it states the task.

Spend about {{.Hours}} hours. The problem is deliberately larger than that
budget, so nobody is expected to finish; use the time well and stop when it
runs out. When you stop, write STOPPING-POINT.md at the root of this
repository: what works, what does not, and what you would do next and why.
{{- if .HasHarness}}

The harness/ directory holds the tooling for exercising your solution; its
files describe how to run it.
{{- end}}

You may use any resource you like, including AI tools.

When you are done, send the repository back as a zip archive or a git
bundle, for example: git bundle create takehome.bundle --all.
`

// sysDesignAbout is the same front page for a design exercise. Everything it
// states is already stated by the brief and the constraint sheet; it exists
// so that whoever opens the drop knows what they are holding before they
// read either.
const sysDesignAbout = `# {{.Title}}

Start with candidate/brief.md; it states the task. candidate/constraints.md
carries the numbers, the rules you have to work inside, and the sections
your document has to cover.

The deliverable is a design document plus whatever diagrams help you make
the case. There is no code to write and nothing to run.

Spend about {{.Hours}} hours. The problem is deliberately larger than that
budget, so nobody is expected to finish; use the time well and stop when it
runs out, and write the STOPPING-POINT.md the brief asks for.

Afterwards we spend an hour working through your design together. Expect the
requirements to change while you are defending it.

You may use any resource you like, including AI tools.

When you are done, send the document and any figures back as a zip archive.
`
