// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/model"
	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/view/cmd"
	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

const traceTitleFmt = "[fg:bg:b] Trace([hilite:bg:b]%s[fg:bg:-])[fg:bg:-] "

// traceRow holds data for one row in the trace table.
type traceRow struct {
	name     string
	resource string
	synced   string
	ready    string
	status   string
	gvr      *client.GVR
	path     string
	isOk     bool
}

// CrossplaneTrace shows a tabular trace of a single Crossplane resource hierarchy,
// matching the layout of `crossplane beta trace`.
type CrossplaneTrace struct {
	*tview.Table

	app     *App
	actions *ui.KeyActions
	gvr     *client.GVR
	fqnPath string
	rows    []traceRow
	wide    bool
}

// NewCrossplaneTrace returns a new trace view.
func NewCrossplaneTrace(app *App, gvr *client.GVR, path string) *CrossplaneTrace {
	return &CrossplaneTrace{
		Table:   tview.NewTable(),
		app:     app,
		actions: ui.NewKeyActions(),
		gvr:     gvr,
		fqnPath: path,
	}
}

func (*CrossplaneTrace) SetCommand(*cmd.Interpreter)            {}
func (*CrossplaneTrace) SetFilter(string, bool)                 {}
func (*CrossplaneTrace) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the trace view.
func (t *CrossplaneTrace) Init(_ context.Context) error {
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

func (*CrossplaneTrace) InCmdMode() bool              { return false }
func (*CrossplaneTrace) Name() string                 { return "Trace" }
func (*CrossplaneTrace) Start()                       {}
func (t *CrossplaneTrace) Stop()                      { t.app.Styles.RemoveListener(t) }
func (t *CrossplaneTrace) Hints() model.MenuHints     { return t.actions.Hints() }
func (*CrossplaneTrace) ExtraHints() map[string]string { return nil }

func (t *CrossplaneTrace) StylesChanged(s *config.Styles) { t.applyStyles(s) }

func (t *CrossplaneTrace) applyStyles(s *config.Styles) {
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

func (t *CrossplaneTrace) updateTitle() {
	_, name := client.Namespaced(t.fqnPath)
	fmat := fmt.Sprintf(traceTitleFmt, name)
	styles := t.app.Styles.Frame()
	t.SetTitle(ui.SkinTitle(fmat, &styles))
}

func (t *CrossplaneTrace) bindKeys() {
	t.actions.Bulk(ui.KeyMap{
		tcell.KeyEscape: ui.NewKeyAction("Back", t.backCmd, false),
		ui.KeyQ:         ui.NewKeyAction("Back", t.backCmd, false),
		ui.KeyD:         ui.NewKeyAction("Describe", t.describeCmd, true),
		ui.KeyY:         ui.NewKeyAction(yamlAction, t.yamlCmd, true),
		ui.KeyW:         ui.NewKeyAction("Wide", t.toggleWideCmd, true),
	})
	if !t.app.Config.IsReadOnly() {
		t.actions.Add(ui.KeyP, ui.NewKeyActionWithOpts("Pause", t.pauseCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
		t.actions.Add(ui.KeyU, ui.NewKeyActionWithOpts("Unpause", t.unpauseCmd,
			ui.ActionOpts{Visible: true, Dangerous: true}))
	}
}

func (t *CrossplaneTrace) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	if a, ok := t.actions.Get(ui.AsKey(evt)); ok {
		return a.Action(evt)
	}
	return evt
}

func (t *CrossplaneTrace) backCmd(evt *tcell.EventKey) *tcell.EventKey {
	return t.app.PrevCmd(evt)
}

func (t *CrossplaneTrace) selectedRow() (traceRow, bool) {
	row, _ := t.GetSelection()
	idx := row - 1
	if idx < 0 || idx >= len(t.rows) {
		return traceRow{}, false
	}
	return t.rows[idx], true
}

func (t *CrossplaneTrace) describeCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	g := dao.Generic{}
	g.Init(t.app.factory, r.gvr)
	desc, err := g.Describe(r.path)
	if err != nil {
		t.app.Flash().Errf("Describe failed: %s", err)
		return nil
	}
	details := NewDetails(t.app, "Describe", r.path, contentYAML, true).Update(desc)
	if err := t.app.inject(details, false); err != nil {
		t.app.Flash().Err(err)
	}
	return nil
}

func (t *CrossplaneTrace) yamlCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	g := dao.Generic{}
	g.Init(t.app.factory, r.gvr)
	raw, err := g.ToYAML(r.path, false)
	if err != nil {
		t.app.Flash().Errf("YAML failed: %s", err)
		return nil
	}
	details := NewDetails(t.app, yamlAction, r.path, contentYAML, true).Update(raw)
	if err := t.app.inject(details, false); err != nil {
		t.app.Flash().Err(err)
	}
	return nil
}

const crossplanePausedAnnotation = "crossplane.io/paused"

func (t *CrossplaneTrace) pauseCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	if err := t.setCrossplanePaused(r.gvr, r.path, true); err != nil {
		t.app.Flash().Errf("Pause failed: %s", err)
		return nil
	}
	t.app.Flash().Infof("Paused %s", r.path)
	t.rebuildTable()
	return nil
}

func (t *CrossplaneTrace) unpauseCmd(evt *tcell.EventKey) *tcell.EventKey {
	r, ok := t.selectedRow()
	if !ok {
		return evt
	}
	if err := t.setCrossplanePaused(r.gvr, r.path, false); err != nil {
		t.app.Flash().Errf("Unpause failed: %s", err)
		return nil
	}
	t.app.Flash().Infof("Unpaused %s", r.path)
	t.rebuildTable()
	return nil
}

func (t *CrossplaneTrace) setCrossplanePaused(gvr *client.GVR, path string, paused bool) error {
	conn := t.app.factory.Client()
	if conn == nil {
		return fmt.Errorf("no client connection")
	}
	dial, err := conn.DynDial()
	if err != nil {
		return err
	}

	ns, name := client.Namespaced(path)
	if client.IsClusterScoped(ns) {
		ns = ""
	}
	res := dial.Resource(gvr.GVR())

	var patch []byte
	if paused {
		patch, _ = json.Marshal(map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{
					crossplanePausedAnnotation: "true",
				},
			},
		})
	} else {
		patch, _ = json.Marshal(map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{
					crossplanePausedAnnotation: nil,
				},
			},
		})
	}

	if ns != "" {
		_, err = res.Namespace(ns).Patch(context.Background(), name, types.MergePatchType, patch, metav1.PatchOptions{})
	} else {
		_, err = res.Patch(context.Background(), name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

func (t *CrossplaneTrace) toggleWideCmd(evt *tcell.EventKey) *tcell.EventKey {
	t.wide = !t.wide
	t.rebuildTable()
	if t.wide {
		t.app.Flash().Info("Wide mode ON")
	} else {
		t.app.Flash().Info("Wide mode OFF")
	}
	return nil
}

func (t *CrossplaneTrace) rebuildTable() {
	t.Clear()
	if err := t.buildTable(); err != nil {
		t.app.Flash().Err(err)
	}
}

// traceEntry is an intermediate struct used during tree collection.
type traceEntry struct {
	kind     string
	name     string
	ns       string
	resource string
	synced   string
	ready    string
	status   string
	gvr      *client.GVR
	isOk     bool
	children []traceEntry
}

// buildTable fetches the resource hierarchy and renders it as a table.
func (t *CrossplaneTrace) buildTable() error {
	ns, name := client.Namespaced(t.fqnPath)
	if client.IsClusterScoped(ns) {
		ns = ""
	}

	root, err := dao.DirectGet(t.app.factory, t.gvr, ns, name)
	if err != nil {
		return fmt.Errorf("failed to fetch %s/%s: %w", t.gvr, t.fqnPath, err)
	}

	// Build the tree structure.
	rootEntry := t.buildEntry(root, t.gvr)

	// Flatten to rows with tree prefixes.
	t.rows = nil
	t.flattenEntry(rootEntry, "", "")

	// Render header — use tview color tags like the standard k9s table.
	headers := []string{"NAME", "RESOURCE", "SYNCED", "READY", "STATUS"}
	headerColor := t.app.Styles.Table().Header.FgColor.String()
	for col, h := range headers {
		text := fmt.Sprintf("[%s::b]%s[-::-]", headerColor, h)
		cell := tview.NewTableCell(text).
			SetExpansion(0).
			SetSelectable(false)
		if col == 0 {
			cell.SetExpansion(1)
		}
		t.SetCell(0, col, cell)
	}

	// Render data rows — use the same color as standard k9s table rows.
	// StdColor (Frame.Status.NewColor) is what the default colorer uses.
	fgColor := t.app.Styles.Frame().Status.NewColor.Color()
	for i, r := range t.rows {
		row := i + 1

		nameColor := fgColor
		if !r.isOk {
			nameColor = tcell.ColorOrangeRed
		}

		// Color SYNCED/READY individually: green for True, orange for False.
		syncedColor := colorForBool(r.synced, fgColor)
		readyColor := colorForBool(r.ready, fgColor)
		statusColor := fgColor
		if !r.isOk {
			statusColor = tcell.ColorOrangeRed
		}

		statusText := r.status
		if !t.wide && len(statusText) > 64 {
			statusText = statusText[:61] + "..."
		}

		t.SetCell(row, 0, tview.NewTableCell(r.name).SetTextColor(nameColor).SetExpansion(1))
		t.SetCell(row, 1, tview.NewTableCell(r.resource).SetTextColor(fgColor))
		t.SetCell(row, 2, tview.NewTableCell(r.synced).SetTextColor(syncedColor))
		t.SetCell(row, 3, tview.NewTableCell(r.ready).SetTextColor(readyColor))
		t.SetCell(row, 4, tview.NewTableCell(statusText).SetTextColor(statusColor).SetExpansion(1))
	}

	t.Select(1, 0)
	return nil
}

// buildEntry recursively builds a traceEntry tree from an unstructured resource.
func (t *CrossplaneTrace) buildEntry(obj *unstructured.Unstructured, gvr *client.GVR) traceEntry {
	synced, ready, message := crossplaneConditions(obj.Object)
	readyReason := crossplaneReadyReason(obj.Object)
	compResource := crossplaneCompositionResource(obj.Object)

	statusText := readyReason
	if message != "" && (synced != "True" || ready != "True") {
		statusText = message
	}

	entry := traceEntry{
		kind:     obj.GetKind(),
		name:     obj.GetName(),
		ns:       obj.GetNamespace(),
		resource: compResource,
		synced:   synced,
		ready:    ready,
		status:   statusText,
		gvr:      gvr,
		isOk:     synced == "True" && ready == "True",
	}

	raw := obj.Object

	// Claim → XR
	if isCrossplaneClaim(raw) {
		entry.children = append(entry.children, t.resolveRef(raw)...)
	}
	// XR → MRs
	if isCrossplaneXR(raw) {
		entry.children = append(entry.children, t.resolveRefs(raw)...)
	}
	// Connection secret
	entry.children = append(entry.children, t.resolveSecret(raw)...)

	return entry
}

func (t *CrossplaneTrace) resolveRef(obj map[string]any) []traceEntry {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	ref, ok := spec["resourceRef"].(map[string]any)
	if !ok {
		return nil
	}

	apiVersion, _ := ref["apiVersion"].(string)
	kind, _ := ref["kind"].(string)
	refName, _ := ref["name"].(string)
	if apiVersion == "" || kind == "" || refName == "" {
		return nil
	}

	childGVR := resolveGVR(apiVersion, kind)
	child, err := dao.DirectGet(t.app.factory, childGVR, "", refName)
	if err != nil || child == nil {
		slog.Warn("Missing resourceRef", slogs.GVR, childGVR, slogs.FQN, refName, slogs.Error, err)
		return []traceEntry{{
			kind: kind, name: refName, gvr: childGVR, status: "MISSING",
		}}
	}

	e := t.buildEntry(child, childGVR)
	return []traceEntry{e}
}

func (t *CrossplaneTrace) resolveRefs(obj map[string]any) []traceEntry {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	refs, ok := spec["resourceRefs"].([]any)
	if !ok {
		return nil
	}

	var entries []traceEntry
	for _, r := range refs {
		ref, ok := r.(map[string]any)
		if !ok {
			continue
		}
		apiVersion, _ := ref["apiVersion"].(string)
		kind, _ := ref["kind"].(string)
		refName, _ := ref["name"].(string)
		refNs, _ := ref["namespace"].(string)
		if apiVersion == "" || kind == "" || refName == "" {
			continue
		}

		childGVR := resolveGVR(apiVersion, kind)
		child, err := dao.DirectGet(t.app.factory, childGVR, refNs, refName)
		if err != nil || child == nil {
			slog.Warn("Missing resourceRefs target", slogs.GVR, childGVR, slogs.FQN, refName, slogs.Error, err)
			entries = append(entries, traceEntry{
				kind: kind, name: refName, ns: refNs, gvr: childGVR, status: "MISSING",
			})
			continue
		}

		entries = append(entries, t.buildEntry(child, childGVR))
	}
	return entries
}

func (t *CrossplaneTrace) resolveSecret(obj map[string]any) []traceEntry {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	secretRef, ok := spec["writeConnectionSecretToRef"].(map[string]any)
	if !ok {
		return nil
	}
	secretName, _ := secretRef["name"].(string)
	secretNs, _ := secretRef["namespace"].(string)
	if secretName == "" {
		return nil
	}

	fqn := secretName
	if secretNs != "" {
		fqn = client.FQN(secretNs, secretName)
	}

	_, err := t.app.factory.Get(client.SecGVR, fqn, true, labels.Everything())
	entry := traceEntry{
		kind:   "Secret",
		name:   secretName,
		ns:     secretNs,
		gvr:    client.SecGVR,
		synced: "True",
		ready:  "True",
		status: "Available",
		isOk:   true,
	}
	if err != nil {
		entry.synced = ""
		entry.ready = ""
		entry.status = "MISSING"
		entry.isOk = false
	}
	return []traceEntry{entry}
}

// colorForBool returns green for "True", orange for "False", and default for anything else.
func colorForBool(val string, defaultColor tcell.Color) tcell.Color {
	switch val {
	case "True":
		return defaultColor
	case "False":
		return tcell.ColorOrangeRed
	default:
		return defaultColor
	}
}

// flattenEntry converts a traceEntry tree into flat rows with tree-drawing prefixes.
// displayPrefix is the tree characters to prepend to this node's name.
// contPrefix is the continuation prefix for this node's children.
func (t *CrossplaneTrace) flattenEntry(e traceEntry, displayPrefix, contPrefix string) {
	fqn := e.name
	if e.ns != "" {
		fqn = client.FQN(e.ns, e.name)
	}

	t.rows = append(t.rows, traceRow{
		name:     displayPrefix + e.kind + "/" + e.name,
		resource: e.resource,
		synced:   e.synced,
		ready:    e.ready,
		status:   e.status,
		gvr:      e.gvr,
		path:     fqn,
		isOk:     e.isOk,
	})

	for i, child := range e.children {
		isLast := i == len(e.children)-1
		var childDisplay, childCont string
		if isLast {
			childDisplay = contPrefix + "└── "
			childCont = contPrefix + "    "
		} else {
			childDisplay = contPrefix + "├── "
			childCont = contPrefix + "│   "
		}
		t.flattenEntry(child, childDisplay, childCont)
	}
}
