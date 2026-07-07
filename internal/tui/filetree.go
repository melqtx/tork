package tui

import (
	"path"
	"sort"
	"strings"

	"github.com/melqtx/tork/internal/engine"
)

type fileNode struct {
	name      string
	children  []*fileNode
	fileIdx   int
	length    int64
	depth     int
	collapsed bool
}

func buildFileTree(files []engine.FileInfo) *fileNode {
	root := &fileNode{fileIdx: -1}
	dirMaps := map[*fileNode]map[string]*fileNode{}
	fileMaps := map[*fileNode]map[string]*fileNode{}

	for _, f := range files {
		parts := splitTorrentPath(f.Path)
		if len(parts) == 0 {
			parts = []string{"?"}
		}
		cur := root
		cur.length += f.Length
		for i, part := range parts {
			last := i == len(parts)-1
			if last {
				if fileMaps[cur] == nil {
					fileMaps[cur] = map[string]*fileNode{}
				}
				n := fileMaps[cur][part]
				if n == nil {
					n = &fileNode{name: part, fileIdx: f.Index}
					fileMaps[cur][part] = n
					cur.children = append(cur.children, n)
				}
				n.length = f.Length
				n.fileIdx = f.Index
				continue
			}
			if dirMaps[cur] == nil {
				dirMaps[cur] = map[string]*fileNode{}
			}
			next := dirMaps[cur][part]
			if next == nil {
				next = &fileNode{name: part, fileIdx: -1}
				dirMaps[cur][part] = next
				cur.children = append(cur.children, next)
			}
			next.length += f.Length
			cur = next
		}
	}
	sortFileTree(root, -1)
	return root
}

func splitTorrentPath(p string) []string {
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if p == "." || p == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

func sortFileTree(n *fileNode, depth int) {
	n.depth = max(0, depth)
	sort.SliceStable(n.children, func(i, j int) bool {
		a, b := n.children[i], n.children[j]
		if (a.fileIdx < 0) != (b.fileIdx < 0) {
			return a.fileIdx < 0
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	for _, c := range n.children {
		sortFileTree(c, depth+1)
	}
}

func (n *fileNode) flatten(out *[]*fileNode) {
	for _, c := range n.children {
		*out = append(*out, c)
		if c.fileIdx < 0 && !c.collapsed {
			c.flatten(out)
		}
	}
}

func (n *fileNode) leafIndices(out *[]int) {
	if n.fileIdx >= 0 {
		*out = append(*out, n.fileIdx)
		return
	}
	for _, c := range n.children {
		c.leafIndices(out)
	}
}

type checkState int

const (
	checkAll checkState = iota
	checkNone
	checkMixed
)

func nodeCheck(n *fileNode, excluded map[int]bool) checkState {
	var leaves []int
	n.leafIndices(&leaves)
	if len(leaves) == 0 {
		return checkNone
	}
	included := 0
	for _, idx := range leaves {
		if !excluded[idx] {
			included++
		}
	}
	switch included {
	case 0:
		return checkNone
	case len(leaves):
		return checkAll
	default:
		return checkMixed
	}
}

func countDirs(n *fileNode) int {
	total := 0
	for _, c := range n.children {
		if c.fileIdx < 0 {
			total++
			total += countDirs(c)
		}
	}
	return total
}
