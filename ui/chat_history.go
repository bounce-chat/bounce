package ui

import (
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"

	"fyne.io/fyne/v2/theme"
)

const jumpToBottomIconSize = 46
const timestampRefreshSeconds = 30
const mergeSeconds = 300

type chatHistory struct {
	widget.BaseWidget
	sync.Mutex

	id       uuid.UUID
	messages *messageStore

	items   []threadable
	ids     []uuid.UUID
	heights []float32

	unread int

	jumpToBottomIcon *clickableImage

	Length     func() int                              `json:"-"`
	CreateItem func() fyne.CanvasObject                `json:"-"`
	UpdateItem func(index int, item fyne.CanvasObject) `json:"-"`

	scroller      *container.Scroll
	offsetY       float32
	offsetUpdated func(fyne.Position)

	windowFocused         func() bool
	readCallback          func(uuid.UUID, string)
	unreadCountCallback   func(int)
	markAllAsReadCallback func(uuid.UUID)
	seenTracking          map[uuid.UUID]bool
}

func newChatHistory(threadID uuid.UUID, messageStore *messageStore, readCallback func(uuid.UUID, string), unreadCountCallback func(int), markAllAsReadCallback func(uuid.UUID), windowFocused func() bool) *chatHistory {
	if readCallback == nil {
		log.Fatal("cannot create chat history widget without read callback")
	}

	ch := &chatHistory{
		id:                    threadID,
		messages:              messageStore,
		items:                 []threadable{},
		ids:                   []uuid.UUID{},
		heights:               []float32{},
		readCallback:          readCallback,
		unreadCountCallback:   unreadCountCallback,
		markAllAsReadCallback: markAllAsReadCallback,
		seenTracking:          make(map[uuid.UUID]bool),
		windowFocused:         windowFocused,
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
	ch.UpdateItem = func(index int, obj fyne.CanvasObject) {
		item := ch.items[index]
		item.populateTemplate(obj)
	}

	ch.jumpToBottomIcon = newClickableImage(
		"",
		newEmbeddedResource("assets/icons/thread/jump_to_bottom.png"),
		jumpToBottomIconSize,
		jumpToBottomIconSize,
		false,
		func() {
			ch.ScrollToBottom()
			ch.unread = 0
			ch.updateUnreadCounter()
			ch.Lock()
			for index, id := range ch.ids {
				ch.items[index].markSeen()
				ch.seenTracking[id] = true
			}
			ch.markAllAsReadCallback(ch.id)
			ch.Unlock()
		},
	)
	ch.jumpToBottomIcon.Hide()

	// Keep chat bubble timestamps up to date by periodically refreshing
	go func() {
		for {
			time.Sleep(timestampRefreshSeconds * time.Second)
			ch.Refresh()
		}
	}()

	ch.ExtendBaseWidget(ch)
	return ch
}

func (ch *chatHistory) setItems(items []threadable, initialSize fyne.Size) {
	ch.items = items
	sizer := ch.CreateItem()

	ch.ids = []uuid.UUID{}
	for i, item := range items {
		ch.ids = append(ch.ids, item.getID())
		ch.messages.insert(item)

		if item.isSeen() {
			ch.seenTracking[item.getID()] = true
		}

		if item.countsAsUnread() && !item.isSeen() {
			ch.unread += 1
		}

		cd, ok := ch.messages.queryCache(item.getID())
		if ok && cd.Width == initialSize.Width && cd.Height != 0 {
			ch.SetItemHeight(i, cd.Height)
			if cd.Merges {
				cbd, ok := item.(*chatBubbleData)
				if !ok {
					log.WithFields(log.Fields{
						"id": item.getID(),
					}).Warn("cached mergable item is not chat bubble data")
					continue
				} else {
					cbd.mergeMode = cd.MergeMode
				}
			}
		} else {
			ch.setMergeMode(i, false)

			ch.UpdateItem(i, sizer)
			sizer.Resize(initialSize)
			height := sizer.MinSize().Height
			ch.SetItemHeight(i, height)
			ch.messages.cacheHeight(item.getID(), initialSize.Width, height)
		}
	}

	ch.Refresh()
}

func (ch *chatHistory) insertItem(ti *threadItem, appendingToEnd bool) {
	ch.Lock()
	defer ch.Unlock()

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
					ch.setMergeMode(0, true)
					break
				}
				compare := ch.items[i-1]
				if ti.timestamp > compare.getTimestamp() {
					ch.items = append(ch.items[:i], append([]threadable{ti.widgetData}, ch.items[i:]...)...)
					ch.ids = append(ch.ids[:i], append([]uuid.UUID{ti.id}, ch.ids[i:]...)...)
					if len(ch.heights) >= len(ch.items)-1 {
						ch.heights = append(ch.heights[:i], append([]float32{0}, ch.heights[i:]...)...)
					}
					ch.setMergeMode(i, true)
					break
				}
			}
		}
	} else {
		ch.items = append(ch.items, ti.widgetData)
		ch.ids = append(ch.ids, ti.id)
		ch.setMergeMode(len(ch.items)-1, true)
	}

	if ti.widgetData.countsAsUnread() && !ti.widgetData.isSeen() {
		ch.unread += 1
	}

	ch.messages.insert(ti.widgetData)
}

func (ch *chatHistory) deleteItem(id uuid.UUID) bool {
	ch.Lock()
	defer ch.Unlock()

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

	ch.messages.remove(id)

	return found
}

func (ch *chatHistory) isLastItem(id uuid.UUID) bool {
	ch.Lock()
	defer ch.Unlock()

	if len(ch.items) == 0 {
		return false
	}

	item := ch.items[len(ch.items)-1]
	return item.getID() == id
}

func (ch *chatHistory) isLastAuthor(id uuid.UUID) bool {
	ch.Lock()
	defer ch.Unlock()

	if len(ch.items) == 0 {
		return false
	}

	item := ch.items[len(ch.items)-1]
	return item.getAuthor() == id
}

func (ch *chatHistory) setMergeMode(index int, neighbors bool) {
	mergeUp := false
	mergeDown := false

	checkUp := true
	checkDown := true

	if len(ch.items)-1 < index {
		log.WithFields(log.Fields{
			"index": index,
		}).Warn("out of bounds index while setting merge mode")
		return
	}
	if len(ch.items)-1 == index {
		checkDown = false
	}
	if index == 0 {
		checkUp = false
	}

	item := ch.items[index]
	myType := item.getType()
	if !(myType == chat.TypeGroupMessage || myType == chat.TypeDirectMessage) {
		return
	}
	myAuthor := item.getAuthor()
	myTimestamp := item.getTimestamp()

	if checkUp {
		above := ch.items[index-1]
		aboveType := above.getType()
		aboveAuthor := above.getAuthor()
		aboveTimestamp := above.getTimestamp()
		if (aboveType == chat.TypeGroupMessage || aboveType == chat.TypeDirectMessage) && aboveAuthor == myAuthor && aboveTimestamp > myTimestamp-mergeSeconds {
			mergeUp = true
		}
	}

	if checkDown {
		below := ch.items[index+1]
		belowType := below.getType()
		belowAuthor := below.getAuthor()
		belowTimestamp := below.getTimestamp()
		if (belowType == chat.TypeGroupMessage || belowType == chat.TypeDirectMessage) && belowAuthor == myAuthor && belowTimestamp < myTimestamp+mergeSeconds {
			mergeDown = true
		}
	}

	cbd, ok := item.(*chatBubbleData)
	if !ok {
		log.WithFields(log.Fields{
			"index": index,
		}).Warn("threadable message does not have type chatBubbleData while setting mergeMode")
		return
	}
	if mergeUp && mergeDown {
		cbd.mergeMode = mergeModeMiddle
	} else if mergeUp && !mergeDown {
		cbd.mergeMode = mergeModeBottom
	} else if !mergeUp && mergeDown {
		cbd.mergeMode = mergeModeTop
	} else if !mergeUp && !mergeDown {
		cbd.mergeMode = mergeModeStandalone
	}
	ch.messages.cacheMergeMode(cbd.id, cbd.mergeMode)

	if neighbors {
		if mergeUp {
			above := ch.items[index-1]
			aboveCBD, ok := above.(*chatBubbleData)
			if !ok {
				log.WithFields(log.Fields{
					"index": index - 1,
				}).Warn("could not cast chat message into chatBubbleData")
				return
			}
			switch cbd.mergeMode {
			case mergeModeTop:
				if aboveCBD.mergeMode == mergeModeMiddle || aboveCBD.mergeMode == mergeModeTop {
					ch.setMergeMode(index-1, true)
				}
			case mergeModeMiddle, mergeModeBottom:
				if !(aboveCBD.mergeMode == mergeModeMiddle || aboveCBD.mergeMode == mergeModeTop) {
					ch.setMergeMode(index-1, true)
				}
			case mergeModeStandalone:
				if !(aboveCBD.mergeMode == mergeModeBottom || aboveCBD.mergeMode == mergeModeStandalone) {
					ch.setMergeMode(index-1, true)
				}
			}
		}

		if mergeDown {
			below := ch.items[index+1]
			belowCBD, ok := below.(*chatBubbleData)
			if !ok {
				log.WithFields(log.Fields{
					"index": index + 1,
				}).Warn("could not cast chat message into chatBubbleData")
				return
			}

			switch cbd.mergeMode {
			case mergeModeTop, mergeModeMiddle:
				if !(belowCBD.mergeMode == mergeModeMiddle || belowCBD.mergeMode == mergeModeBottom) {
					ch.setMergeMode(index+1, true)
				}
			case mergeModeBottom, mergeModeStandalone:
				if !(belowCBD.mergeMode == mergeModeTop || belowCBD.mergeMode == mergeModeStandalone) {
					ch.setMergeMode(index+1, true)
				}
			}
		}
	}
}

func (ch *chatHistory) headTimestamp() int64 {
	ch.Lock()
	defer ch.Unlock()

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
	ch.Lock()
	defer ch.Unlock()

	id := ch.ids[index]
	if _, ok := ch.seenTracking[id]; !ok {
		ch.seenTracking[id] = true

		item := ch.items[index]
		item.markSeen()
		if item.countsAsUnread() {
			ch.unread -= 1
		}
		if ch.windowFocused() {
			go ch.readCallback(id, item.getType())
		}
	}

	ch.updateUnreadCounter()
}

func (ch *chatHistory) updateUnreadCounter() {
	ch.unreadCountCallback(ch.unread)
}

func (ch *chatHistory) MinSize() fyne.Size {
	ch.ExtendBaseWidget(ch)
	return ch.BaseWidget.MinSize()
}

func (ch *chatHistory) RefreshItem(index int) {
	if ch.scroller == nil {
		return
	}
	ch.BaseWidget.Refresh()
	lo := ch.scroller.Content.(*fyne.Container).Layout.(*chatHistoryLayout)
	lo.renderLock.RLock() // ensures we are not changing visible info in render code during the search
	item, ok := lo.searchVisible(lo.visible, index)
	lo.renderLock.RUnlock()
	if ok {
		lo.setupItem(item, index)
	}
}

func (ch *chatHistory) SetItemHeight(index int, height float32) {
	ch.Lock()

	if !(len(ch.heights) > index) {
		missing := index + 1 - len(ch.heights)
		ch.heights = append(ch.heights, make([]float32, missing)...)
	}
	refresh := ch.heights[index] != height
	ch.heights[index] = height

	ch.Unlock()

	if refresh {
		ch.RefreshItem(index)
	}
}

func (ch *chatHistory) getItemHeight(index int) (float32, bool) {
	if !(len(ch.heights) > index) {
		return 0, false
	}
	if ch.heights[index] == 0 {
		return 0, false
	}
	return ch.heights[index], true
}

func (ch *chatHistory) offsetFor(index int) float32 {
	y := float32(0)
	separatorThickness := ch.Theme().Size(theme.SizeNamePadding)
	for i := 0; i <= index; i++ {
		ch.Lock()
		height, ok := ch.getItemHeight(i)
		ch.Unlock()

		if ok {
			y += height + separatorThickness
		} else {
			height = ch.calculateAndSetItemHeight(i, ch.Size())
			y += height + separatorThickness
		}
	}
	y -= separatorThickness

	h := ch.scroller.Size().Height
	if h == 0 && !fyne.CurrentDevice().IsMobile() {
		h = chatContainerSizeAtStartup().Height
	}

	return y - h
}

func (ch *chatHistory) scrollTo(index int) {
	if ch.scroller == nil {
		return
	}

	ch.scroller.Offset.Y = ch.offsetFor(index)
	ch.offsetUpdated(ch.scroller.Offset)
}

func (ch *chatHistory) Resize(s fyne.Size) {
	ch.BaseWidget.Resize(s)
	if ch.scroller == nil {
		return
	}

	ch.offsetUpdated(ch.scroller.Offset)
	ch.scroller.Content.(*fyne.Container).Layout.(*chatHistoryLayout).updateList(true)
}

func (ch *chatHistory) ScrollTo(id int) {
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

func (ch *chatHistory) ScrollToOffset(offset float32) {
	if ch.scroller == nil {
		return
	}
	if offset < 0 {
		offset = 0
	}
	contentHeight := ch.contentMinSize().Height
	if ch.Size().Height >= contentHeight {
		return
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

func (ch *chatHistory) displayJumpToBottomIfNeeded() {
	if ch.scroller == nil {
		return
	}
	scrollerHeight := ch.scroller.Size().Height
	if scrollerHeight == 0 {
		scrollerHeight = chatContainerSizeAtStartup().Height
	}
	if ch.offsetY < ch.contentHeight()-scrollerHeight*2.5 {
		ch.jumpToBottomIcon.Show()
	} else {
		ch.jumpToBottomIcon.Hide()
	}
}

func (ch *chatHistory) contentMinSize() fyne.Size {
	scrollerHeight := ch.scroller.Size().Height
	if scrollerHeight == 0 {
		scrollerHeight = chatContainerSizeAtStartup().Height
	}
	return fyne.NewSize(
		0,
		ch.offsetFor(len(ch.items)-1)+scrollerHeight,
	)
}

func (ch *chatHistory) contentHeight() float32 {
	if ch.scroller == nil {
		return 0
	}
	return ch.scroller.Content.(*fyne.Container).Size().Height
}

func (ch *chatHistory) calculateAndSetItemHeight(id int, containerSize fyne.Size) float32 {
	sizer := ch.CreateItem()
	ch.UpdateItem(id, sizer)
	sizer.Resize(containerSize)
	height := sizer.MinSize().Height
	ch.SetItemHeight(id, height)
	if len(ch.ids) > id {
		ch.messages.cacheHeight(ch.ids[id], containerSize.Width, height)
	}
	return height
}

func (ch *chatHistory) CreateRenderer() fyne.WidgetRenderer {
	ch.ExtendBaseWidget(ch)

	layout := &fyne.Container{Layout: newListLayout(ch)}
	ch.scroller = container.NewVScroll(layout)
	ch.scroller.OnScrolled = ch.offsetUpdated
	layout.Resize(layout.MinSize())
	return newChatHistoryRenderer(ch, layout)
}

type chatHistoryRenderer struct {
	//cachedWidth  float32
	//cachedHeight float32
	ch     *chatHistory
	layout *fyne.Container
}

func newChatHistoryRenderer(ch *chatHistory, layout *fyne.Container) *chatHistoryRenderer {
	chr := &chatHistoryRenderer{
		//cachedWidth:  float32(fyne.CurrentApp().Preferences().Float("chat-width")),
		//cachedHeight: float32(fyne.CurrentApp().Preferences().Float("chat-height")),
		ch:     ch,
		layout: layout,
	}

	return chr
}

func (chr *chatHistoryRenderer) Layout(size fyne.Size) {
	chr.ch.scroller.Resize(size)
	if chr.ch.jumpToBottomIcon.Visible() {
		chr.ch.jumpToBottomIcon.Resize(fyne.Size{jumpToBottomIconSize, jumpToBottomIconSize})
		chr.ch.jumpToBottomIcon.Move(fyne.Position{
			X: size.Width - jumpToBottomIconSize - theme.Padding()*3,
			Y: size.Height - jumpToBottomIconSize - theme.Padding()*3,
		})
	}
}

func (chr *chatHistoryRenderer) MinSize() fyne.Size {
	return chr.ch.scroller.MinSize()
}

func (chr *chatHistoryRenderer) Refresh() {
	//size := chr.ch.Size()
	//if (size.Width != 0 && size.Height != 0) && (chr.cachedWidth != size.Width || chr.cachedHeight != size.Height) {
	//	fyne.CurrentApp().Preferences().SetFloat("chat-width", float64(size.Width))
	//	fyne.CurrentApp().Preferences().SetFloat("chat-height", float64(size.Height))
	//	chr.cachedWidth = size.Width
	//	chr.cachedHeight = size.Height
	//	log.WithFields(log.Fields{
	//		"chat-width":  size.Width,
	//		"chat-height": size.Height,
	//	}).Warn("saving real size")
	//}

	chr.Layout(chr.ch.Size())
	chr.ch.scroller.Refresh()
	layout := chr.layout.Layout.(*chatHistoryLayout)
	layout.updateList(false)

	canvas.Refresh(chr.ch)
}

func (chr *chatHistoryRenderer) Destroy() {
}

func (chr chatHistoryRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		chr.ch.scroller,
		chr.ch.jumpToBottomIcon,
	}
}

type itemAndIndex struct {
	item  fyne.CanvasObject
	index int
}

type chatHistoryLayout struct {
	ch       *chatHistory
	children []fyne.CanvasObject
	visible  []itemAndIndex

	itemPool          sync.Pool
	slicePool         sync.Pool
	visibleRowHeights []float32
	renderLock        sync.RWMutex
}

func newListLayout(ch *chatHistory) fyne.Layout {
	l := &chatHistoryLayout{ch: ch}
	l.slicePool.New = func() any {
		s := make([]itemAndIndex, 0)
		return &s
	}
	ch.offsetUpdated = l.offsetUpdated
	return l
}

func (chl *chatHistoryLayout) calculateVisibleRowHeights(length int, th fyne.Theme) (offY float32, minRow int) {
	rowOffset := float32(0)
	isVisible := false
	chl.visibleRowHeights = chl.visibleRowHeights[:0]

	if chl.ch.scroller.Size().Height <= 0 {
		return
	}

	padding := th.Size(theme.SizeNamePadding)

	for i := 0; i < length; i++ {
		var height float32
		if h, ok := chl.ch.getItemHeight(i); ok {
			height = h
		} else {
			height = chl.ch.calculateAndSetItemHeight(i, chl.ch.Size())
		}

		if rowOffset <= chl.ch.offsetY-height-padding {
			// before scroll
		} else if rowOffset <= chl.ch.offsetY {
			minRow = i
			offY = rowOffset
			isVisible = true
		}
		if rowOffset >= chl.ch.offsetY+chl.ch.scroller.Size().Height {
			break
		}

		rowOffset += height + padding
		if isVisible {
			chl.visibleRowHeights = append(chl.visibleRowHeights, height)
		}
	}
	return
}

func (chl *chatHistoryLayout) Layout([]fyne.CanvasObject, fyne.Size) {
	chl.updateList(true)
}

func (chl *chatHistoryLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return chl.ch.contentMinSize()
}

func (chl *chatHistoryLayout) getItem() fyne.CanvasObject {
	var item fyne.CanvasObject
	o := chl.itemPool.Get()
	if o != nil {
		item = o.(fyne.CanvasObject)
	}
	if item == nil {
		if f := chl.ch.CreateItem; f != nil {
			item = f()
		}
	}
	return item
}

func (chl *chatHistoryLayout) offsetUpdated(pos fyne.Position) {
	if chl.ch.offsetY == pos.Y {
		return
	}

	chl.ch.offsetY = pos.Y
	chl.updateList(true)

	chl.ch.displayJumpToBottomIfNeeded()

	if pos.Y == chl.ch.contentHeight()-chl.ch.scroller.Size().Height {
		if chl.ch.unread > 0 {
			chl.ch.Lock()
			if chl.ch.unread > 0 {
				chl.ch.markAllAsReadCallback(chl.ch.id)
				chl.ch.unread = 0
				chl.ch.updateUnreadCounter()

				for index, id := range chl.ch.ids {
					chl.ch.items[index].markSeen()
					chl.ch.seenTracking[id] = true
				}
			}
			chl.ch.Unlock()
		}
	}
}

func (chl *chatHistoryLayout) setupItem(item fyne.CanvasObject, index int) {
	if f := chl.ch.UpdateItem; f != nil {
		f(index, item)
		chl.ch.SetItemHeight(index, item.MinSize().Height)
		if len(chl.ch.ids) > index {
			chl.ch.messages.cacheHeight(chl.ch.ids[index], chl.ch.Size().Width, item.MinSize().Height)
		}
	}
}

func (chl *chatHistoryLayout) updateList(newOnly bool) {
	th := chl.ch.Theme()
	separatorThickness := th.Size(theme.SizeNamePadding)
	chl.renderLock.Lock()
	width := chl.ch.Size().Width
	length := 0
	if f := chl.ch.Length; f != nil {
		length = f()
	}
	if chl.ch.UpdateItem == nil {
		fyne.LogError("Missing UpdateCell callback required for List", nil)
	}

	// Keep pointer reference for copying slice header when returning to the pool
	// https://blog.mike.norgate.xyz/unlocking-go-slice-performance-navigating-sync-pool-for-enhanced-efficiency-7cb63b0b453e
	wasVisiblePtr := chl.slicePool.Get().(*[]itemAndIndex)
	wasVisible := (*wasVisiblePtr)[:0]
	wasVisible = append(wasVisible, chl.visible...)

	chl.ch.Lock()
	offY, minRow := chl.calculateVisibleRowHeights(length, th)
	chl.ch.Unlock()
	if len(chl.visibleRowHeights) == 0 && length > 0 { // we can't show anything until we have some dimensions
		chl.renderLock.Unlock() // user code should not be locked
		return
	}

	oldVisibleLen := len(chl.visible)
	chl.visible = chl.visible[:0]
	oldChildrenLen := len(chl.children)
	chl.children = chl.children[:0]

	y := offY
	for index, itemHeight := range chl.visibleRowHeights {
		row := index + minRow
		size := fyne.NewSize(width, itemHeight)

		c, ok := chl.searchVisible(wasVisible, row)
		if !ok {
			c = chl.getItem()
			if c == nil {
				continue
			}
			c.Resize(size)
		}

		c.Move(fyne.NewPos(0, y))
		c.Resize(size)

		y += itemHeight + separatorThickness
		chl.visible = append(chl.visible, itemAndIndex{index: row, item: c})
		chl.children = append(chl.children, c)
	}
	chl.nilOldSliceData(chl.children, len(chl.children), oldChildrenLen)
	chl.nilOldVisibleSliceData(chl.visible, len(chl.visible), oldVisibleLen)

	for _, wasVis := range wasVisible {
		if _, ok := chl.searchVisible(chl.visible, wasVis.index); !ok {
			chl.itemPool.Put(wasVis.item)
		}
	}

	c := chl.ch.scroller.Content.(*fyne.Container)
	oldObjLen := len(c.Objects)
	c.Objects = c.Objects[:0]
	c.Objects = append(c.Objects, chl.children...)
	chl.nilOldSliceData(c.Objects, len(c.Objects), oldObjLen)

	// make a local deep copy of chl.visible since rest of this function is unlocked
	// and cannot safely access chl.visible
	visiblePtr := chl.slicePool.Get().(*[]itemAndIndex)
	visible := (*visiblePtr)[:0]
	visible = append(visible, chl.visible...)
	chl.renderLock.Unlock() // user code should not be locked

	if newOnly {
		for _, vis := range visible {
			if _, ok := chl.searchVisible(wasVisible, vis.index); !ok {
				chl.setupItem(vis.item, vis.index)
			}

			offset := chl.ch.GetScrollOffset()
			if offset >= chl.ch.offsetFor(vis.index) {
				chl.ch.seen(vis.index)
			}
		}
	} else {
		for _, vis := range visible {
			chl.setupItem(vis.item, vis.index)

			offset := chl.ch.GetScrollOffset()
			if offset >= chl.ch.offsetFor(vis.index) {
				chl.ch.seen(vis.index)
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
	chl.slicePool.Put(wasVisiblePtr)
	chl.slicePool.Put(visiblePtr)
}

// invariant: visible is in ascending order of IDs
func (chl *chatHistoryLayout) searchVisible(visible []itemAndIndex, index int) (fyne.CanvasObject, bool) {
	ln := len(visible)
	idx := sort.Search(ln, func(i int) bool { return visible[i].index >= index })
	if idx < ln && visible[idx].index == index {
		return visible[idx].item, true
	}
	return nil, false
}

func (chl *chatHistoryLayout) nilOldSliceData(objs []fyne.CanvasObject, len, oldLen int) {
	if oldLen > len {
		objs = objs[:oldLen] // gain view into old data
		for i := len; i < oldLen; i++ {
			objs[i] = nil
		}
	}
}

func (chl *chatHistoryLayout) nilOldVisibleSliceData(objs []itemAndIndex, len, oldLen int) {
	if oldLen > len {
		objs = objs[:oldLen] // gain view into old data
		for i := len; i < oldLen; i++ {
			objs[i].item = nil
		}
	}
}
