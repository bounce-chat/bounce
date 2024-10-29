package ui

// The chat history widget is based on the Fyne widget.List, with modifications to support variable-sized widgets,
// remove unused features and behaviors, and support other callbacks and behaviors

import (
	"math"
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"fyne.io/fyne/v2/theme"
)

// ListItemID uniquely identifies an item within a list.
type ListItemID = int

// List is a widget that pools list items for performance and
// lays the items out in a vertical direction inside of a scroller.
// By default, List requires that all items are the same size, but specific
// rows can have their heights set with SetItemHeight.
//
// Since: 1.4
type List struct {
	widget.BaseWidget

	Length     func() int                                  `json:"-"`
	CreateItem func() fyne.CanvasObject                    `json:"-"`
	UpdateItem func(id ListItemID, item fyne.CanvasObject) `json:"-"`

	currentFocus  ListItemID
	focused       bool
	scroller      *container.Scroll
	itemMin       fyne.Size
	itemHeights   map[ListItemID]float32
	offsetY       float32
	offsetUpdated func(fyne.Position)

	propertyLock sync.RWMutex // TODO: expose the one in BaseWidget?
}

// NewList creates and returns a list widget for displaying items in
// a vertical layout with scrolling and caching for performance.
//
// Since: 1.4
func NewList(length func() int, createItem func() fyne.CanvasObject, updateItem func(ListItemID, fyne.CanvasObject)) *List {
	list := &List{Length: length, CreateItem: createItem, UpdateItem: updateItem}
	list.ExtendBaseWidget(list)
	return list
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (l *List) CreateRenderer() fyne.WidgetRenderer {
	l.ExtendBaseWidget(l)

	if f := l.CreateItem; f != nil && l.itemMin.IsZero() {
		item := createItemAndApplyThemeScope(f, l)

		l.itemMin = item.MinSize()
	}

	layout := &fyne.Container{Layout: newListLayout(l)}
	l.scroller = container.NewVScroll(layout)
	layout.Resize(layout.MinSize())
	objects := []fyne.CanvasObject{l.scroller}
	return newListRenderer(objects, l, l.scroller, layout)
}

// FocusGained is called after this List has gained focus.
//
// Implements: fyne.Focusable
func (l *List) FocusGained() {
	l.focused = true
	l.scrollTo(l.currentFocus)
	l.RefreshItem(l.currentFocus)
}

// FocusLost is called after this List has lost focus.
//
// Implements: fyne.Focusable
func (l *List) FocusLost() {
	l.focused = false
	l.RefreshItem(l.currentFocus)
}

// MinSize returns the size that this widget should not shrink below.
func (l *List) MinSize() fyne.Size {
	l.ExtendBaseWidget(l)
	return l.BaseWidget.MinSize()
}

// RefreshItem refreshes a single item, specified by the item ID passed in.
//
// Since: 2.4
func (l *List) RefreshItem(id ListItemID) {
	if l.scroller == nil {
		return
	}
	l.BaseWidget.Refresh()
	lo := l.scroller.Content.(*fyne.Container).Layout.(*listLayout)
	lo.renderLock.RLock() // ensures we are not changing visible info in render code during the search
	item, ok := lo.searchVisible(lo.visible, id)
	lo.renderLock.RUnlock()
	if ok {
		lo.setupListItem(item, id, l.focused && l.currentFocus == id)
	}
}

// SetItemHeight supports changing the height of the specified list item. Items normally take the height of the template
// returned from the CreateItem callback. The height parameter uses the same units as a fyne.Size type and refers
// to the internal content height not including the divider size.
//
// Since: 2.3
func (l *List) SetItemHeight(id ListItemID, height float32) {
	l.propertyLock.Lock()

	if l.itemHeights == nil {
		l.itemHeights = make(map[ListItemID]float32)
	}

	refresh := l.itemHeights[id] != height
	l.itemHeights[id] = height
	l.propertyLock.Unlock()

	if refresh {
		l.RefreshItem(id)
	}
}

func (l *List) offsetFor(id ListItemID) float32 {
	y := float32(0)
	separatorThickness := l.Theme().Size(theme.SizeNamePadding)
	for i := 0; i <= id; i++ {
		if height, ok := l.itemHeights[i]; ok {
			y += height + separatorThickness
		} else {
			height = l.calculateAndSetItemHeight(id)
			y += height + separatorThickness
		}
	}
	y -= separatorThickness

	return y - l.scroller.Size().Height
}

func (l *List) scrollTo(id ListItemID) {
	if l.scroller == nil {
		return
	}

	y := l.offsetFor(id)
	if y < l.scroller.Offset.Y {
		l.scroller.Offset.Y = y
	} else {
		l.scroller.Offset.Y = l.contentHeight() - l.scroller.Size().Height
	}
	l.offsetUpdated(l.scroller.Offset)
}

// Resize is called when this list should change size. We refresh to ensure invisible items are drawn.
func (l *List) Resize(s fyne.Size) {
	l.BaseWidget.Resize(s)
	if l.scroller == nil {
		return
	}

	l.offsetUpdated(l.scroller.Offset)
	l.scroller.Content.(*fyne.Container).Layout.(*listLayout).updateList(true)
}

// ScrollTo scrolls to the item represented by id
//
// Since: 2.1
func (l *List) ScrollTo(id ListItemID) {
	length := 0
	if f := l.Length; f != nil {
		length = f()
	}
	if id < 0 || id >= length {
		return
	}
	l.scrollTo(id)
	l.Refresh()
}

// ScrollToBottom scrolls to the end of the list
//
// Since: 2.1
func (l *List) ScrollToBottom() {
	length := 0
	if f := l.Length; f != nil {
		length = f()
	}
	if length > 0 {
		length--
	}
	l.scrollTo(length)
	l.Refresh()
}

// ScrollToTop scrolls to the start of the list
//
// Since: 2.1
func (l *List) ScrollToTop() {
	l.scrollTo(0)
	l.Refresh()
}

// ScrollToOffset scrolls the list to the given offset position.
//
// Since: 2.5
func (l *List) ScrollToOffset(offset float32) {
	if l.scroller == nil {
		return
	}
	if offset < 0 {
		offset = 0
	}
	contentHeight := l.contentMinSize().Height
	if l.Size().Height >= contentHeight {
		return // content fully visible - no need to scroll
	}
	if offset > contentHeight {
		offset = contentHeight
	}
	l.scroller.Offset.Y = offset
	l.offsetUpdated(l.scroller.Offset)
	l.Refresh()
}

// GetScrollOffset returns the current scroll offset position
//
// Since: 2.5
func (l *List) GetScrollOffset() float32 {
	return l.offsetY
}

// TypedKey is called if a key event happens while this List is focused.
//
// Implements: fyne.Focusable
func (l *List) TypedKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeyDown:
		if f := l.Length; f != nil && l.currentFocus >= f()-1 {
			return
		}
		l.RefreshItem(l.currentFocus)
		l.currentFocus++
		l.scrollTo(l.currentFocus)
		l.RefreshItem(l.currentFocus)
	case fyne.KeyUp:
		if l.currentFocus <= 0 {
			return
		}
		l.RefreshItem(l.currentFocus)
		l.currentFocus--
		l.scrollTo(l.currentFocus)
		l.RefreshItem(l.currentFocus)
	}
}

// TypedRune is called if a text event happens while this List is focused.
//
// Implements: fyne.Focusable
func (l *List) TypedRune(_ rune) {
	// intentionally left blank
}

func (l *List) contentMinSize() fyne.Size {
	separatorThickness := l.Theme().Size(theme.SizeNamePadding)
	l.propertyLock.Lock()
	defer l.propertyLock.Unlock()
	if l.Length == nil {
		return fyne.NewSize(0, 0)
	}
	items := l.Length()

	if l.itemHeights == nil || len(l.itemHeights) == 0 {
		return fyne.NewSize(l.itemMin.Width,
			(l.itemMin.Height+separatorThickness)*float32(items)-separatorThickness)
	}

	height := float32(0)
	totalCustom := 0
	templateHeight := l.itemMin.Height
	for id, itemHeight := range l.itemHeights {
		if id < items {
			totalCustom++
			height += itemHeight
		}
	}
	height += float32(items-totalCustom) * templateHeight

	return fyne.NewSize(l.itemMin.Width, height+separatorThickness*float32(items-1))
}

func (l *List) contentHeight() float32 {
	if l.scroller == nil {
		return 0
	}
	return l.scroller.Content.(*fyne.Container).Size().Height
}

func (l *List) setItemHeights(currentSize fyne.Size) {
	sizer := l.CreateItem()
	for i := 0; i < l.Length(); i++ {
		l.UpdateItem(i, sizer)
		sizer.Resize(currentSize)
		l.SetItemHeight(i, sizer.MinSize().Height)
	}
}

func (l *List) calculateAndSetItemHeight(id int) float32 {
	sizer := l.CreateItem()
	l.UpdateItem(id, sizer)
	sizer.Resize(l.Size())
	height := sizer.MinSize().Height
	l.SetItemHeight(id, height)
	return height
}

// fills l.visibleRowHeights and also returns offY and minRow
func (l *listLayout) calculateVisibleRowHeights(itemHeight float32, length int, th fyne.Theme) (offY float32, minRow int) {
	rowOffset := float32(0)
	isVisible := false
	l.visibleRowHeights = l.visibleRowHeights[:0]

	if l.list.scroller.Size().Height <= 0 {
		return
	}

	padding := th.Size(theme.SizeNamePadding)

	if len(l.list.itemHeights) == 0 {
		paddedItemHeight := itemHeight + padding

		offY = float32(math.Floor(float64(l.list.offsetY/paddedItemHeight))) * paddedItemHeight
		minRow = int(math.Floor(float64(offY / paddedItemHeight)))
		maxRow := int(math.Ceil(float64((offY + l.list.scroller.Size().Height) / paddedItemHeight)))

		if minRow > length-1 {
			minRow = length - 1
		}
		if minRow < 0 {
			minRow = 0
			offY = 0
		}

		if maxRow > length-1 {
			maxRow = length - 1
		}

		for i := 0; i <= maxRow-minRow; i++ {
			l.visibleRowHeights = append(l.visibleRowHeights, itemHeight)
		}
		return
	}

	for i := 0; i < length; i++ {
		height := itemHeight
		if h, ok := l.list.itemHeights[i]; ok {
			height = h
		}

		if rowOffset <= l.list.offsetY-height-padding {
			// before scroll
		} else if rowOffset <= l.list.offsetY {
			minRow = i
			offY = rowOffset
			isVisible = true
		}
		if rowOffset >= l.list.offsetY+l.list.scroller.Size().Height {
			break
		}

		rowOffset += height + padding
		if isVisible {
			l.visibleRowHeights = append(l.visibleRowHeights, height)
		}
	}
	return
}

// Declare conformity with WidgetRenderer interface.
var _ fyne.WidgetRenderer = (*listRenderer)(nil)

type listRenderer struct {
	BaseRenderer

	list     *List
	scroller *container.Scroll
	layout   *fyne.Container
}

func newListRenderer(objects []fyne.CanvasObject, l *List, scroller *container.Scroll, layout *fyne.Container) *listRenderer {
	lr := &listRenderer{BaseRenderer: NewBaseRenderer(objects), list: l, scroller: scroller, layout: layout}
	lr.scroller.OnScrolled = l.offsetUpdated
	return lr
}

func (l *listRenderer) Layout(size fyne.Size) {
	l.scroller.Resize(size)
}

func (l *listRenderer) MinSize() fyne.Size {
	return l.scroller.MinSize().Max(l.list.itemMin)
}

func (l *listRenderer) Refresh() {
	if f := l.list.CreateItem; f != nil {
		item := createItemAndApplyThemeScope(f, l.list)
		l.list.itemMin = item.MinSize()
	}
	l.Layout(l.list.Size())
	l.scroller.Refresh()
	layout := l.layout.Layout.(*listLayout)
	layout.updateList(false)

	canvas.Refresh(l.list)
}

type listItem struct {
	widget.BaseWidget

	onTapped func()
	child    fyne.CanvasObject
}

func newListItem(child fyne.CanvasObject, tapped func()) *listItem {
	li := &listItem{
		child:    child,
		onTapped: tapped,
	}

	li.ExtendBaseWidget(li)
	return li
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (li *listItem) CreateRenderer() fyne.WidgetRenderer {
	li.ExtendBaseWidget(li)

	objects := []fyne.CanvasObject{li.child}

	return &listItemRenderer{NewBaseRenderer(objects), li}
}

// MinSize returns the size that this widget should not shrink below.
func (li *listItem) MinSize() fyne.Size {
	li.ExtendBaseWidget(li)
	return li.BaseWidget.MinSize()
}

// MouseMoved is called when a desktop pointer hovers over the widget.
func (li *listItem) MouseMoved(*desktop.MouseEvent) {
}

// Tapped is called when a pointer tapped event is captured and triggers any tap handler.
func (li *listItem) Tapped(*fyne.PointEvent) {
	if li.onTapped != nil {
		li.Refresh()
		li.onTapped()
	}
}

// Declare conformity with the WidgetRenderer interface.
var _ fyne.WidgetRenderer = (*listItemRenderer)(nil)

type listItemRenderer struct {
	BaseRenderer

	item *listItem
}

// MinSize calculates the minimum size of a listItem.
// This is based on the size of the status indicator and the size of the child object.
func (li *listItemRenderer) MinSize() fyne.Size {
	return li.item.child.MinSize()
}

// Layout the components of the listItem widget.
func (li *listItemRenderer) Layout(size fyne.Size) {
	//li.item.background.Resize(size)
	li.item.child.Resize(size)
}

func (li *listItemRenderer) Refresh() {
	canvas.Refresh(li.item)
}

type listItemAndID struct {
	item *listItem
	id   ListItemID
}

type listLayout struct {
	list     *List
	children []fyne.CanvasObject

	itemPool          syncPool
	visible           []listItemAndID
	slicePool         sync.Pool // *[]itemAndID
	visibleRowHeights []float32
	renderLock        sync.RWMutex
}

func newListLayout(list *List) fyne.Layout {
	l := &listLayout{list: list}
	l.slicePool.New = func() any {
		s := make([]listItemAndID, 0)
		return &s
	}
	list.offsetUpdated = l.offsetUpdated
	return l
}

func (l *listLayout) Layout([]fyne.CanvasObject, fyne.Size) {
	l.updateList(true)
}

func (l *listLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return l.list.contentMinSize()
}

func (l *listLayout) getItem() *listItem {
	item := l.itemPool.Obtain()
	if item == nil {
		if f := l.list.CreateItem; f != nil {
			item2 := createItemAndApplyThemeScope(f, l.list)

			item = newListItem(item2, nil)
		}
	}
	return item.(*listItem)
}

func (l *listLayout) offsetUpdated(pos fyne.Position) {
	if l.list.offsetY == pos.Y {
		return
	}

	l.list.offsetY = pos.Y
	l.updateList(true)
}

func (l *listLayout) setupListItem(li *listItem, id ListItemID, focus bool) {
	if f := l.list.UpdateItem; f != nil {
		f(id, li.child)
		l.list.SetItemHeight(id, li.child.MinSize().Height)
	}
}

func (l *listLayout) updateList(newOnly bool) {
	th := l.list.Theme()
	separatorThickness := th.Size(theme.SizeNamePadding)
	l.renderLock.Lock()
	width := l.list.Size().Width
	length := 0
	if f := l.list.Length; f != nil {
		length = f()
	}
	if l.list.UpdateItem == nil {
		fyne.LogError("Missing UpdateCell callback required for List", nil)
	}

	// Keep pointer reference for copying slice header when returning to the pool
	// https://blog.mike.norgate.xyz/unlocking-go-slice-performance-navigating-sync-pool-for-enhanced-efficiency-7cb63b0b453e
	wasVisiblePtr := l.slicePool.Get().(*[]listItemAndID)
	wasVisible := (*wasVisiblePtr)[:0]
	wasVisible = append(wasVisible, l.visible...)

	l.list.propertyLock.Lock()
	offY, minRow := l.calculateVisibleRowHeights(l.list.itemMin.Height, length, th)
	l.list.propertyLock.Unlock()
	if len(l.visibleRowHeights) == 0 && length > 0 { // we can't show anything until we have some dimensions
		l.renderLock.Unlock() // user code should not be locked
		return
	}

	oldVisibleLen := len(l.visible)
	l.visible = l.visible[:0]
	oldChildrenLen := len(l.children)
	l.children = l.children[:0]

	y := offY
	for index, itemHeight := range l.visibleRowHeights {
		row := index + minRow
		size := fyne.NewSize(width, itemHeight)

		c, ok := l.searchVisible(wasVisible, row)
		if !ok {
			c = l.getItem()
			if c == nil {
				continue
			}
			c.Resize(size)
		}

		c.Move(fyne.NewPos(0, y))
		c.Resize(size)

		y += itemHeight + separatorThickness
		l.visible = append(l.visible, listItemAndID{id: row, item: c})
		l.children = append(l.children, c)
	}
	l.nilOldSliceData(l.children, len(l.children), oldChildrenLen)
	l.nilOldVisibleSliceData(l.visible, len(l.visible), oldVisibleLen)

	for _, wasVis := range wasVisible {
		if _, ok := l.searchVisible(l.visible, wasVis.id); !ok {
			l.itemPool.Release(wasVis.item)
		}
	}

	c := l.list.scroller.Content.(*fyne.Container)
	oldObjLen := len(c.Objects)
	c.Objects = c.Objects[:0]
	c.Objects = append(c.Objects, l.children...)
	l.nilOldSliceData(c.Objects, len(c.Objects), oldObjLen)

	// make a local deep copy of l.visible since rest of this function is unlocked
	// and cannot safely access l.visible
	visiblePtr := l.slicePool.Get().(*[]listItemAndID)
	visible := (*visiblePtr)[:0]
	visible = append(visible, l.visible...)
	l.renderLock.Unlock() // user code should not be locked

	if newOnly {
		for _, vis := range visible {
			if _, ok := l.searchVisible(wasVisible, vis.id); !ok {
				l.setupListItem(vis.item, vis.id, l.list.focused && l.list.currentFocus == vis.id)
			}
			//TODO: if unread
			//offset := l.list.GetScrollOffset()
			//if offset >= l.list.offsetFor(vis.id) {
			//	log.WithFields(log.Fields{
			//		"id": vis.id,
			//	}).Warn("read message")
			//}
		}
	} else {
		for _, vis := range visible {
			l.setupListItem(vis.item, vis.id, l.list.focused && l.list.currentFocus == vis.id)
			//TODO: if unread
			//offset := l.list.GetScrollOffset()
			//if offset >= l.list.offsetFor(vis.id) {
			//	log.WithFields(log.Fields{
			//		"id": vis.id,
			//	}).Warn("read message")
			//}
		}
	}

	// nil out all references before returning slices to pool
	for i := 0; i < len(wasVisible); i++ {
		wasVisible[i].item = nil
	}
	for i := 0; i < len(visible); i++ {
		visible[i].item = nil
	}
	*wasVisiblePtr = wasVisible // Copy the stack header over to the heap
	*visiblePtr = visible
	l.slicePool.Put(wasVisiblePtr)
	l.slicePool.Put(visiblePtr)
}

// invariant: visible is in ascending order of IDs
func (l *listLayout) searchVisible(visible []listItemAndID, id ListItemID) (*listItem, bool) {
	ln := len(visible)
	idx := sort.Search(ln, func(i int) bool { return visible[i].id >= id })
	if idx < ln && visible[idx].id == id {
		return visible[idx].item, true
	}
	return nil, false
}

func (l *listLayout) nilOldSliceData(objs []fyne.CanvasObject, len, oldLen int) {
	if oldLen > len {
		objs = objs[:oldLen] // gain view into old data
		for i := len; i < oldLen; i++ {
			objs[i] = nil
		}
	}
}

func (l *listLayout) nilOldVisibleSliceData(objs []listItemAndID, len, oldLen int) {
	if oldLen > len {
		objs = objs[:oldLen] // gain view into old data
		for i := len; i < oldLen; i++ {
			objs[i].item = nil
		}
	}
}

func createItemAndApplyThemeScope(f func() fyne.CanvasObject, scope fyne.Widget) fyne.CanvasObject {
	item := f()
	//if !cache.OverrideThemeMatchingScope(item, scope) {
	//	return item
	//}

	item.Refresh()
	return item
}

type BaseRenderer struct { // TODO: promote these?
	objects []fyne.CanvasObject
}

// NewBaseRenderer creates a new BaseRenderer.
func NewBaseRenderer(objects []fyne.CanvasObject) BaseRenderer {
	return BaseRenderer{objects}
}

// Destroy does nothing in the base implementation.
//
// Implements: fyne.WidgetRenderer
func (r *BaseRenderer) Destroy() {
}

// Objects returns the objects that should be rendered.
//
// Implements: fyne.WidgetRenderer
func (r *BaseRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

// SetObjects updates the objects of the renderer.
func (r *BaseRenderer) SetObjects(objects []fyne.CanvasObject) {
	r.objects = objects
}

type syncPool struct {
	sync.Pool
}

// Obtain returns an item from the pool for use
func (p *syncPool) Obtain() (item fyne.CanvasObject) {
	o := p.Get()
	if o != nil {
		item = o.(fyne.CanvasObject)
	}
	return
}

// Release adds an item into the pool to be used later
func (p *syncPool) Release(item fyne.CanvasObject) {
	p.Put(item)
}
