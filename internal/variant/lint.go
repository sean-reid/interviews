package variant

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"
	"text/template/parse"
)

// TemplateIssue is one problem found in candidate-facing template text.
type TemplateIssue struct {
	Path string
	Msg  string
}

// textExtensions are the candidate files rendered as templates. Binary
// fixtures and datasets pass through untouched, so they are not checked.
var textExtensions = map[string]bool{
	".md": true, ".txt": true, ".yaml": true, ".yml": true, ".json": true,
}

// CheckTemplates parses every candidate-visible text file and reports
// unparseable templates and references to parameters the problem does not
// declare. Without this, a typo like {{.scal}} only surfaces when a bundle is
// rendered for a real candidate.
func CheckTemplates(fsys fs.FS, candidateFiles []string, declared map[string]bool) []TemplateIssue {
	var issues []TemplateIssue
	for _, name := range candidateFiles {
		if !textExtensions[strings.ToLower(path.Ext(name))] {
			continue
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			issues = append(issues, TemplateIssue{Path: name, Msg: fmt.Sprintf("cannot read: %v", err)})
			continue
		}
		tmpl, err := template.New(name).Parse(string(raw))
		if err != nil {
			issues = append(issues, TemplateIssue{Path: name, Msg: fmt.Sprintf("template does not parse: %v", err)})
			continue
		}
		for _, ref := range fieldRefs(tmpl) {
			if !declared[ref] {
				issues = append(issues, TemplateIssue{Path: name,
					Msg: fmt.Sprintf("references {{.%s}}, which the manifest does not declare", ref)})
			}
		}
	}
	return issues
}

// fieldRefs returns the distinct top-level field names a template reads.
func fieldRefs(tmpl *template.Template) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tmpl.Templates() {
		if t.Tree == nil || t.Root == nil {
			continue
		}
		walkNode(t.Root, func(f *parse.FieldNode) {
			if len(f.Ident) == 0 {
				return
			}
			name := f.Ident[0]
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		})
	}
	return out
}

func walkNode(n parse.Node, visit func(*parse.FieldNode)) {
	switch node := n.(type) {
	case nil:
		return
	case *parse.FieldNode:
		visit(node)
	case *parse.ListNode:
		if node == nil {
			return
		}
		for _, child := range node.Nodes {
			walkNode(child, visit)
		}
	case *parse.ActionNode:
		walkNode(node.Pipe, visit)
	case *parse.PipeNode:
		if node == nil {
			return
		}
		for _, cmd := range node.Cmds {
			walkNode(cmd, visit)
		}
	case *parse.CommandNode:
		for _, arg := range node.Args {
			walkNode(arg, visit)
		}
	case *parse.IfNode:
		walkBranch(&node.BranchNode, visit)
	case *parse.RangeNode:
		walkBranch(&node.BranchNode, visit)
	case *parse.WithNode:
		walkBranch(&node.BranchNode, visit)
	case *parse.TemplateNode:
		walkNode(node.Pipe, visit)
	}
}

func walkBranch(b *parse.BranchNode, visit func(*parse.FieldNode)) {
	walkNode(b.Pipe, visit)
	walkNode(b.List, visit)
	walkNode(b.ElseList, visit)
}
