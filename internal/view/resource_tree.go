// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/model"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/view/cmd"
	"github.com/derailed/k9s/internal/view/tree"
	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"k8s.io/apimachinery/pkg/labels"
)

const treeTitleFmt = "[fg:bg:b] Tree/%s([hilite:bg:b]%s[fg:bg:-])[fg:bg:-] "

// treeRow is the flattened view of a tree.Node, carrying tree-drawing prefixes
// and provider-defined column values.
type treeRow struct {
	label   string
	columns []string
	node    *tree.Node
}

// ResourceTree is a generic, provider-driven tree view. The underlying
// hierarchy traversal and column semantics are delegated to a tree.Provider,
// so this type handles only layout, navigation, styling, and common actions.
type ResourceTree struct {
	*tview.Table

	app      *App
	actions  *ui.KeyActions
	gvr      *client.GVR
	fqnPath  string
	provider tree.Provider
	rows     []treeRow
	wide     bool
}

// NewResourceTree returns a new tree view for a specific provider.
func NewResourceTree(app *App, gvr *client.GVR, path string, p tree.Provider) *ResourceTree {
	return &ResourceTree{
		Table:    tview.NewTable(),
		app:      app,
		actions:  ui.NewKeyActions(),
		gvr:      gvr,
		fqnPath:  path,
		provider: p,
	}
}

// --- noop ResourceViewer implementations ---

func (*ResourceTree) SetCommand(*cmd.Interpreter)            {}
func (*ResourceTree) SetFilter(string, bool)                 {}
func (*ResourceTree) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the tree view.
func (t *ResourceTree) Init(_ context.Context) error {
	t.SetBorder(true)
	t.SetBorderPadding(0, 0, 1, 1)
	t.SetSelectable(true, false)
	t.SetFixed(1, 0)

	t.app.Styles.AddListener(t)
	t.applyStyles(t.app.Styles)
	t.updateTitle()
	t.bindKeys()
	t.SetInputCapture(t.keyboard)

	if err := t.buildTable(); err != nil {
		return err
	}
	return nil
}

func (*ResourceTree) InCmdMode() bool                  { return false }
func (*ResourceTree) Name() string                     { return "Tree" }
func (*ResourceTree) Start()                           {}
func (t *ResourceTree) Stop()                          { t.app.Styles.RemoveListener(t) }
func (t *ResourceTree) Hints() model.MenuHints         { return t.actions.Hints() }
func (*ResourceTree) ExtraHints() map[string]string    { return nil }
func (t *ResourceTree) StylesChanged(s *config.Styles) { t.applyStyles(s) }

func (t *ResourceTree) applyStyles(s *config.Styles) {
	t.SetBackgroundColor(s.Table().BgColor.Color())
	t.SetBorderColor(s.Frame().Border.FgColor.Color())
	t.SetBorderFocusColor(s.Frame().Border.FocusColor.Color())
	t.SetSelectedStyle(
		tcell.StyleDefault.
			Foreground(s.Table().CursorFgColor.Color()).
			Background(s.Table().CursorBgColor.Color()).
			Attributes(tcell.AttrBold),
	)
}

func (t *ResourceTree) updateTitle() {
	_, name := client.Namespaced(t.fqnPath)
	fmat := fmt.Sprintf(treeTitleFmt, t.provider.DisplayName(), name)
	styles := t.app.Styles.Frame()
	t.SetTitle(ui.SkinTitle(fmat, &styles))
}

func (t *ResourceTree) bindKeys() {
	t.actions.Bulk(ui.KeyMap{
		tcell.KeyEscape: ui.NewKeyAction("Back", t.backCmd, false),
		ui.KeyQ:         ui.NewKeyAction("Back", t.backCmd, false),
		ui.KeyD:         ui.NewKeyAction("Describe", t.describeCmd, true),
		ui.KeyY:         ui.NewKeyAction(yamlAction, t.yamlCmd, true),
		ui.KeyW:         ui.NewKeyAction("Wide", t.toggleWideCmd, true),
	})
	// Optional pause/unpause is surfaced only when the provider supports it
	// and the app is not read-only.
	if _, ok := t.provider.(tree.PausableProvider); ok && !t.app.Config.IsReadOnly() {
		t.actions.Add(ui.KeyP, ui.NewKeyActionWithOpts("Pause", t.pauseCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
		t.actions.Add(ui.KeyU, ui.NewKeyActionWithOpts("Unpause", t.unpauseCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
	}
}

func (t *ResourceTree) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	if a, ok := t.actions.Get(ui.AsKey(evt)); ok {
		return a.Action(evt)
	}
	return evt
}

func (t *ResourceTree) backCmd(evt *tcell.EventKey) *tcell.EventKey {
	return t.app.PrevCmd(evt)
}

func (t *ResourceTree) selectedRow() (treeRow, bool) {
	row, _ := t.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(t.rows) {
		return treeRow{}, false
	}
	return t.rows[idx], true
}

func (t *ResourceTree) describeCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	g := dao.Generic{}
	g.Init(t.app.factory, r.node.GVR)
	desc, err := g.Describe(r.node.FQN())
	if err != nil {
		t.app.Flash().Errf("Describe failed: %s", err)
		return nil
	}
	details := NewDetails(t.app, "Describe", r.node.FQN(), contentYAML, true).Update(desc)
	if err := t.app.inject(details, false); err != nil {
		t.app.Flash().Err(err)
	}
	return nil
}

func (t *ResourceTree) yamlCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	g := dao.Generic{}
	g.Init(t.app.factory, r.node.GVR)
	raw, err := g.ToYAML(r.node.FQN(), false)
	if err != nil {
		t.app.Flash().Errf("YAML failed: %s", err)
		return nil
	}
	details := NewDetails(t.app, yamlAction, r.node.FQN(), contentYAML, true).Update(raw)
	if err := t.app.inject(details, false); err != nil {
		t.app.Flash().Err(err)
	}
	return nil
}

func (t *ResourceTree) pauseCmd(evt *tcell.EventKey) *tcell.EventKey {
	return t.togglePause(evt, true)
}

func (t *ResourceTree) unpauseCmd(evt *tcell.EventKey) *tcell.EventKey {
	return t.togglePause(evt, false)
}

func (t *ResourceTree) togglePause(evt *tcell.EventKey, paused bool) *tcell.EventKey {
	pp, ok := t.provider.(tree.PausableProvider)
	if !ok {
		return evt
	}
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	if !pp.SupportsPause(r.node) {
		t.app.Flash().Warnf("Pause not supported for this node")
		return nil
	}
	if err := pp.SetPaused(context.Background(), t.app.factory, r.node, paused); err != nil {
		verb := "Unpause"
		if paused {
			verb = "Pause"
		}
		t.app.Flash().Errf("%s failed: %s", verb, err)
		return nil
	}
	if paused {
		t.app.Flash().Infof("Paused %s", r.node.FQN())
	} else {
		t.app.Flash().Infof("Unpaused %s", r.node.FQN())
	}
	t.rebuildTable()
	return nil
}

func (t *ResourceTree) toggleWideCmd(_ *tcell.EventKey) *tcell.EventKey {
	t.wide = !t.wide
	t.rebuildTable()
	if t.wide {
		t.app.Flash().Info("Wide mode ON")
	} else {
		t.app.Flash().Info("Wide mode OFF")
	}
	return nil
}

func (t *ResourceTree) rebuildTable() {
	t.Clear()
	if err := t.buildTable(); err != nil {
		t.app.Flash().Err(err)
	}
}

// buildTable fetches the root, builds the Node tree via the provider, and
// renders the flattened rows with tree-drawing prefixes.
func (t *ResourceTree) buildTable() error {
	ns, name := client.Namespaced(t.fqnPath)
	if client.IsClusterScoped(ns) {
		ns = ""
	}

	root, err := dao.DirectGet(t.app.factory, t.gvr, ns, name)
	if err != nil {
		return fmt.Errorf("failed to fetch %s/%s: %w", t.gvr, t.fqnPath, err)
	}

	node, err := t.provider.BuildRoot(context.Background(), t.app.factory, t.gvr, root)
	if err != nil {
		return fmt.Errorf("provider %q failed to build tree: %w", t.provider.ID(), err)
	}
	if node == nil {
		return fmt.Errorf("provider %q returned an empty tree", t.provider.ID())
	}

	t.rows = nil
	t.flattenNode(node, "", "")

	cols := append([]string{"NAME"}, t.provider.Columns()...)
	headerColor := t.app.Styles.Table().Header.FgColor.String()
	for i, h := range cols {
		text := fmt.Sprintf("[%s::b]%s[-::-]", headerColor, h)
		cell := tview.NewTableCell(text).
			SetExpansion(0).
			SetSelectable(false)
		if i == 0 {
			cell.SetExpansion(1)
		}
		t.SetCell(0, i, cell)
	}

	fgColor := t.app.Styles.Frame().Status.NewColor.Color()
	for i, r := range t.rows {
		row := i + 1
		nameColor := fgColor
		if !r.node.IsOk {
			nameColor = tcell.ColorOrangeRed
		}
		t.SetCell(row, 0, tview.NewTableCell(r.label).SetTextColor(nameColor).SetExpansion(1))

		for j, v := range r.columns {
			lastCol := j == len(r.columns)-1
			c := t.colorFor(j, v, r.node.IsOk, fgColor, lastCol)
			text := v
			if !t.wide && lastCol && len(text) > 64 {
				text = text[:61] + "..."
			}
			cell := tview.NewTableCell(text).SetTextColor(c)
			if lastCol {
				cell.SetExpansion(1)
			}
			t.SetCell(row, j+1, cell)
		}
	}

	t.Select(1, 0)
	return nil
}

// flattenNode walks the tree depth-first and appends rows with the canonical
// ASCII tree prefixes.
func (t *ResourceTree) flattenNode(n *tree.Node, displayPrefix, contPrefix string) {
	if n == nil {
		return
	}
	label := displayPrefix + n.Kind + "/" + n.Name
	cols := n.Columns
	if cols == nil {
		cols = make([]string, len(t.provider.Columns()))
	}
	t.rows = append(t.rows, treeRow{label: label, columns: cols, node: n})

	for i, child := range n.Children {
		isLast := i == len(n.Children)-1
		var childDisplay, childCont string
		if isLast {
			childDisplay = contPrefix + "└── "
			childCont = contPrefix + "    "
		} else {
			childDisplay = contPrefix + "├── "
			childCont = contPrefix + "│   "
		}
		t.flattenNode(child, childDisplay, childCont)
	}
}

// colorFor delegates the per-cell color decision to the active provider.
// Providers that implement tree.StatusProvider get to drive coloring for
// their own status terms; otherwise tree.DefaultStatus is used. The
// last column (STATUS) additionally inherits the row's ok-ness when no
// explicit kind is known, matching the prior behavior.
func (t *ResourceTree) colorFor(idx int, val string, isOk bool, defaultColor tcell.Color, lastCol bool) tcell.Color {
	var kind tree.StatusKind
	if sp, ok := t.provider.(tree.StatusProvider); ok {
		kind = sp.Status(idx, val)
	} else {
		kind = tree.DefaultStatus(val)
	}
	switch kind {
	case tree.StatusOk:
		return defaultColor
	case tree.StatusError:
		return tcell.ColorOrangeRed
	case tree.StatusWarn:
		return tcell.ColorYellow
	}
	if lastCol && !isOk && val != "" && val != "-" {
		return tcell.ColorOrangeRed
	}
	return defaultColor
}
