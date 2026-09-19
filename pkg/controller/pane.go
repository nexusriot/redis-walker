package controller

import (
	"strings"

	"github.com/rivo/tview"

	"github.com/nexusriot/redis-walker/pkg/model"
)

// pane is one browser: a Redis connection, the directory it shows and the list
// widget that renders it.
type pane struct {
	idx   int
	model *model.Model
	list  *tview.List
	// owned is set when the controller created the connection and must close it.
	owned bool

	prefix   string
	nodes    map[string]*model.Node
	ordered  []string
	position map[string]int
	stats    map[string]*model.DirStats
}

func newPane(idx int, m *model.Model, list *tview.List, owned bool) *pane {
	return &pane{
		idx:      idx,
		model:    m,
		list:     list,
		owned:    owned,
		nodes:    make(map[string]*model.Node),
		position: make(map[string]int),
		stats:    make(map[string]*model.DirStats),
	}
}

// mapKeyOf builds a list item key that is unique even when a directory and a
// plain key share the same name ("a" and "a/b" both exist).
func mapKeyOf(n *model.Node) string {
	if n.IsDir {
		return n.Key + "|dir"
	}
	return n.Key + "|file"
}

// setNodes replaces the listing of a pane.
func (p *pane) setNodes(nodes []*model.Node) {
	m := make(map[string]*model.Node, len(nodes))
	for _, n := range nodes {
		m[mapKeyOf(n)] = n
	}
	p.nodes = m
}

// sortedMapKeys returns directory keys first, then leaf keys, in the order the
// list shows them. Search and "jump" rely on this being the only ordering.
func (p *pane) sortedMapKeys() (dirs, files []string) {
	nodes := make([]*model.Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		nodes = append(nodes, n)
	}
	for _, n := range model.SortNodes(nodes) {
		if n.IsDir {
			dirs = append(dirs, mapKeyOf(n))
			continue
		}
		files = append(files, mapKeyOf(n))
	}
	return dirs, files
}

// selected returns the node under the cursor of this pane.
func (p *pane) selected() (*model.Node, bool) {
	if p.list.GetItemCount() == 0 {
		return nil, false
	}
	_, mk := p.list.GetItemText(p.list.GetCurrentItem())
	mk = strings.TrimSpace(mk)
	if mk == upItem {
		return nil, false
	}
	n, ok := p.nodes[mk]
	return n, ok
}

// indexOf returns the list index of a display label, or -1.
func (p *pane) indexOf(label string) int {
	for i, v := range p.ordered {
		if v == label {
			return i + 1 // account for "[..]"
		}
	}
	return -1
}

// dir renders the directory of this pane as a display path.
func (p *pane) dir() string { return model.DisplayDir(p.prefix) }
