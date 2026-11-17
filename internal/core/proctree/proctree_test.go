package proctree

import (
	"os/exec"
	"testing"
)

func TestNilAndUnstartedTrees(t *testing.T) {
	var nilTree *Tree
	if err := nilTree.Kill(); err != nil {
		t.Fatal(err)
	}
	nilTree.Release()
	cmd := exec.Command("does-not-matter")
	tree := New(cmd)
	tree.Attach() // not started
	if err := tree.Kill(); err != nil {
		t.Fatal(err)
	}
	tree.Release()
	tree.Release()
}
