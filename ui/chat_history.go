package ui

// The chat history widget is based on the Fyne widget.List, with modifications to support variable-sized widgets,
// remove unused features and behaviors, and support other callbacks and behaviors

import (
	"sort"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"fyne.io/fyne/v2/theme"
)

// ListItemID uniquely identifies an item within a list.
type ListItemID = int

type chatHistory struct {
	widget.BaseWidget

	items      []threadable
	ids        []uuid.UUID
	heights    []float32
	itemsMutex sync.Mutex

	Length     func() int                                  `json:"-"`
	CreateItem func() fyne.CanvasObject                    `json:"-"`
	UpdateItem func(id ListItemID, item fyne.CanvasObject) `json:"-"`

	currentFocus ListItemID // TODO: still used?
	focused      bool       // TODO: still used?
	itemMin      fyne.Size  // TODO: still used?

	scroller      *container.Scroll
	offsetY       float32
	offsetUpdated func(fyne.Position)

	readCallback        func(uuid.UUID, string)
	unreadCountCallback func(int)
	seenTracking        map[uuid.UUID]bool

	propertyLock sync.RWMutex // TODO: expose the one in BaseWidget?
}

func newChatHistory(readCallback func(uuid.UUID, string), unreadCountCallback func(int)) *chatHistory {
	if readCallback == nil {
		log.Fatal("cannot create chat history widget without read callback")
	}

	ch := &chatHistory{
		items:               []threadable{},
		ids:                 []uuid.UUID{},
		heights:             []float32{},
		readCallback:        readCallback,
		unreadCountCallback: unreadCountCallback,
		seenTracking:        make(map[uuid.UUID]bool),
	}
	ch.Length = func() int {
		return len(ch.items)
	}
	ch.CreateItem = func() fyne.CanvasObject {
		return container.NewStack(
			newChatBubbleTemplate(),
			newStatusChangeTemplate(),
		)
	}
	ch.UpdateItem = func(id widget.ListItemID, obj fyne.CanvasObject) {
		item := ch.items[id]
		item.populateTemplate(obj)
	}
	ch.ExtendBaseWidget(ch)
	return ch
}

func (ch *chatHistory) setItems(items []threadable) {
	messagesMutex.Lock()
	defer messagesMutex.Unlock()

	ch.itemsMutex.Lock()
	defer ch.itemsMutex.Unlock()

	ch.items = items
	ch.Refresh()

	ids := []uuid.UUID{}
	for _, item := range items {
		if item.isSeen() {
			ch.seenTracking[item.getID()] = true
		}
		ids = append(ids, item.getID())

		messages[item.getID()] = item
	}
	ch.ids = ids
}

func (ch *chatHistory) insertItem(ti *threadItem, appendingToEnd bool) {
	ch.itemsMutex.Lock()
	defer ch.itemsMutex.Unlock()

	// Insert statusChange thread items into their correct location in time, insert everything else
	// at the bottom regaurdless of timestamp
	if _, ok := ti.widgetData.(*statusChangeData); ok {
		if len(ch.items) == 0 || appendingToEnd {
			ch.items = append(ch.items, ti.widgetData)
			ch.ids = append(ch.ids, ti.id)
		} else {
			for i := len(ch.items); i >= 0; i-- {
				if i == 0 {
					ch.items = append([]threadable{ti.widgetData}, ch.items...)
					ch.ids = append([]uuid.UUID{ti.id}, ch.ids...)
					ch.heights = append([]float32{0}, ch.heights...)
					break
				}
				var compareTime int64
				compare := ch.items[i-1]
				switch cast := compare.(type) {
				case *chatBubbleData:
					compareTime = cast.writtenAt
				case *statusChangeData:
					compareTime = cast.timestamp
				default:
					log.Warn("cannot compare timestamp with unknown thread item type")
					continue
				}
				if ti.timestamp > compareTime {
					ch.items = append(ch.items[:i], append([]threadable{ti.widgetData}, ch.items[i:]...)...)
					ch.ids = append(ch.ids[:i], append([]uuid.UUID{ti.id}, ch.ids[i:]...)...)
					ch.heights = append(ch.heights[:i], append([]float32{0}, ch.heights[i:]...)...)
					break
				}
			}
		}
	} else {
		ch.items = append(ch.items, ti.widgetData)
		ch.ids = append(ch.ids, ti.id)
	}

	messagesMutex.Lock()
	messages[ti.widgetData.getID()] = ti.widgetData
	messagesMutex.Unlock()
}

func (ch *chatHistory) deleteItem(id uuid.UUID) bool {
	ch.itemsMutex.Lock()
	defer ch.itemsMutex.Unlock()

	found := false
	location := 0
	for i, obj := range ch.items {
		switch cast := obj.(type) {
		case *chatBubbleData:
			if cast.id == id {
				found = true
				location = i
				break
			}
		case *statusChangeData:
			if cast.id == id {
				found = true
				location = i
				break
			}
		default:
			continue
		}
	}

	ch.items = append(ch.items[:location], ch.items[location+1:]...)
	ch.ids = append(ch.ids[:location], ch.ids[location+1:]...)
	ch.heights = append(ch.heights[:location], ch.heights[location+1:]...)

	ch.Refresh()

	messagesMutex.Lock()
	delete(messages, id)
	messagesMutex.Unlock()

	return found
}

func (ch *chatHistory) headTimestamp() int64 {
	ch.itemsMutex.Lock()
	defer ch.itemsMutex.Unlock()

	currentHead := int64(0)
	if len(ch.items) > 0 {
		compare := ch.items[len(ch.items)-1]
		switch cast := compare.(type) {
		case *chatBubbleData:
			currentHead = cast.writtenAt
		case *statusChangeData:
			currentHead = cast.timestamp
		default:
			log.Warn("cannot compare timestamp with unknown thread item type")
		}
	}

	return currentHead
}

func (ch *chatHistory) seen(index int) {
	ch.itemsMutex.Lock()
	defer ch.itemsMutex.Unlock()

	id := ch.ids[index]
	if _, ok := ch.seenTracking[id]; !ok {
		ch.seenTracking[id] = true

		item := ch.items[index]
		item.markSeen()
		go ch.readCallback(id, item.getType())
	}

	ch.updateUnreadCounter()
}

func (ch *chatHistory) updateUnreadCounter() {
	unread := 0
	for _, item := range ch.items {
		if !item.isSeen() && item.countsAsUnread() {
			unread += 1
		}
	}
	ch.unreadCountCallback(unread)
}

func (ch *chatHistory) scrollToLastRead() {
	for i, item := range ch.items {
		if !item.isSeen() {
			if i == 0 {
				ch.ScrollToTop()
			} else {
				ch.ScrollTo(i - 1)
			}
			return
		}
	}
	ch.ScrollToBottom()
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (ch *chatHistory) CreateRenderer() fyne.WidgetRenderer {
	ch.ExtendBaseWidget(ch)

	if f := ch.CreateItem; f != nil && ch.itemMin.IsZero() {
		item := createItemAndApplyThemeScope(f, ch)

		ch.itemMin = item.MinSize()
	}

	layout := &fyne.Container{Layout: newListLayout(ch)}
	ch.scroller = container.NewVScroll(layout)
	layout.Resize(layout.MinSize())
	objects := []fyne.CanvasObject{ch.scroller}
	return newListRenderer(objects, ch, ch.scroller, layout)
}

func (ch *chatHistory) FocusGained() {
	ch.focused = true
	ch.scrollTo(ch.currentFocus)
	ch.RefreshItem(ch.currentFocus)
}

func (ch *chatHistory) FocusLost() {
	ch.focused = false
	ch.RefreshItem(ch.currentFocus)
}

func (ch *chatHistory) MinSize() fyne.Size {
	ch.ExtendBaseWidget(ch)
	return ch.BaseWidget.MinSize()
}

func (ch *chatHistory) RefreshItem(id ListItemID) {
	if ch.scroller == nil {
		return
	}
	ch.BaseWidget.Refresh()
	lo := ch.scroller.Content.(*fyne.Container).Layout.(*listLayout)
	lo.renderLock.RLock() // ensures we are not changing visible info in render code during the search
	item, ok := lo.searchVisible(lo.visible, id)
	lo.renderLock.RUnlock()
	if ok {
		lo.setupListItem(item, id, ch.focused && ch.currentFocus == id)
	}
}

func (ch *chatHistory) SetItemHeight(id ListItemID, height float32) {
	ch.propertyLock.Lock()

	if !(len(ch.heights) > id) {
		missing := id + 1 - len(ch.heights)
		ch.heights = append(ch.heights, make([]float32, missing)...)
	}
	refresh := ch.heights[id] != height
	ch.heights[id] = height

	ch.propertyLock.Unlock()

	if refresh {
		ch.RefreshItem(id)
	}
}

func (ch *chatHistory) getItemHeight(id ListItemID) (float32, bool) {
	if !(len(ch.heights) > id) {
		return 0, false
	}
	if ch.heights[id] == 0 {
		return 0, false
	}
	return ch.heights[id], true
}

func (ch *chatHistory) offsetFor(id ListItemID) float32 {
	y := float32(0)
	separatorThickness := ch.Theme().Size(theme.SizeNamePadding)
	for i := 0; i <= id; i++ {
		ch.propertyLock.Lock()
		height, ok := ch.getItemHeight(i)
		ch.propertyLock.Unlock()

		if ok {
			y += height + separatorThickness
		} else {
			height = ch.calculateAndSetItemHeight(i)
			y += height + separatorThickness
		}
	}
	y -= separatorThickness

	return y - ch.scroller.Size().Height
}

func (ch *chatHistory) scrollTo(id ListItemID) {
	if ch.scroller == nil {
		return
	}

	ch.scroller.Offset.Y = ch.offsetFor(id)
	ch.offsetUpdated(ch.scroller.Offset)
}

func (ch *chatHistory) Resize(s fyne.Size) {
	ch.BaseWidget.Resize(s)
	if ch.scroller == nil {
		return
	}

	ch.offsetUpdated(ch.scroller.Offset)
	ch.scroller.Content.(*fyne.Container).Layout.(*listLayout).updateList(true)
}

func (ch *chatHistory) ScrollTo(id ListItemID) {
	length := 0
	if f := ch.Length; f != nil {
		length = f()
	}
	if id < 0 || id >= length {
		return
	}
	ch.scrollTo(id)
	ch.Refresh()
}

func (ch *chatHistory) ScrollToBottom() {
	length := 0
	if f := ch.Length; f != nil {
		length = f()
	}
	if length > 0 {
		length--
	}
	ch.scrollTo(length)
	ch.Refresh()
}

func (ch *chatHistory) ScrollToTop() {
	ch.scrollTo(0)
	ch.Refresh()
}

func (ch *chatHistory) ScrollToOffset(offset float32) {
	if ch.scroller == nil {
		return
	}
	if offset < 0 {
		offset = 0
	}
	contentHeight := ch.contentMinSize().Height
	if ch.Size().Height >= contentHeight {
		return // content fully visible - no need to scroll
	}
	if offset > contentHeight {
		offset = contentHeight
	}
	ch.scroller.Offset.Y = offset
	ch.offsetUpdated(ch.scroller.Offset)
	ch.Refresh()
}

func (ch *chatHistory) GetScrollOffset() float32 {
	return ch.offsetY
}

func (ch *chatHistory) TypedKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeyDown:
		if f := ch.Length; f != nil && ch.currentFocus >= f()-1 {
			return
		}
		ch.RefreshItem(ch.currentFocus)
		ch.currentFocus++
		ch.scrollTo(ch.currentFocus)
		ch.RefreshItem(ch.currentFocus)
	case fyne.KeyUp:
		if ch.currentFocus <= 0 {
			return
		}
		ch.RefreshItem(ch.currentFocus)
		ch.currentFocus--
		ch.scrollTo(ch.currentFocus)
		ch.RefreshItem(ch.currentFocus)
	}
}

func (ch *chatHistory) TypedRune(_ rune) {
	// intentionally left blank
}

func (ch *chatHistory) contentMinSize() fyne.Size { // TODO: is this the same as offsetFor(len(data)-1)?
	separatorThickness := ch.Theme().Size(theme.SizeNamePadding)
	ch.propertyLock.Lock()
	defer ch.propertyLock.Unlock()
	if ch.Length == nil {
		return fyne.NewSize(0, 0)
	}
	items := ch.Length()

	height := float32(0)
	totalCustom := 0
	templateHeight := ch.itemMin.Height
	for id, itemHeight := range ch.heights {
		if id < items {
			totalCustom++
			height += itemHeight
		}
	}
	height += float32(items-totalCustom) * templateHeight

	return fyne.NewSize(ch.itemMin.Width, height+separatorThickness*float32(items-1))
}

func (ch *chatHistory) contentHeight() float32 {
	if ch.scroller == nil {
		return 0
	}
	return ch.scroller.Content.(*fyne.Container).Size().Height
}

func (ch *chatHistory) setItemHeights(currentSize fyne.Size) {
	sizer := ch.CreateItem()
	for i := 0; i < ch.Length(); i++ {
		ch.UpdateItem(i, sizer)
		sizer.Resize(currentSize)
		ch.SetItemHeight(i, sizer.MinSize().Height)
	}
}

func (ch *chatHistory) calculateAndSetItemHeight(id int) float32 {
	sizer := ch.CreateItem()
	ch.UpdateItem(id, sizer)
	sizer.Resize(ch.Size())
	height := sizer.MinSize().Height
	ch.SetItemHeight(id, height)
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

	for i := 0; i < length; i++ {
		height := itemHeight
		if h, ok := l.list.getItemHeight(i); ok {
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

	list     *chatHistory
	scroller *container.Scroll
	layout   *fyne.Container
}

func newListRenderer(objects []fyne.CanvasObject, ch *chatHistory, scroller *container.Scroll, layout *fyne.Container) *listRenderer {
	lr := &listRenderer{BaseRenderer: NewBaseRenderer(objects), list: ch, scroller: scroller, layout: layout}
	lr.scroller.OnScrolled = ch.offsetUpdated
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
	list     *chatHistory
	children []fyne.CanvasObject

	itemPool          syncPool
	visible           []listItemAndID
	slicePool         sync.Pool // *[]itemAndID
	visibleRowHeights []float32
	renderLock        sync.RWMutex
}

func newListLayout(ch *chatHistory) fyne.Layout {
	l := &listLayout{list: ch}
	l.slicePool.New = func() any {
		s := make([]listItemAndID, 0)
		return &s
	}
	ch.offsetUpdated = l.offsetUpdated
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

			offset := l.list.GetScrollOffset()
			if offset >= l.list.offsetFor(vis.id) {
				l.list.seen(vis.id)
			}
		}
	} else {
		for _, vis := range visible {
			l.setupListItem(vis.item, vis.id, l.list.focused && l.list.currentFocus == vis.id)

			offset := l.list.GetScrollOffset()
			if offset >= l.list.offsetFor(vis.id) {
				l.list.seen(vis.id)
			}
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
