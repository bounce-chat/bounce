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

	id       uuid.UUID
	myID     uuid.UUID
	messages *messageStore
	added    map[uuid.UUID]bool

	ids     []uuid.UUID
	heights []float32

	disableSeenTracking bool
	unread              int

	jumpToBottomIcon *clickableImage

	template func() fyne.CanvasObject

	scroller      *container.Scroll
	offsetY       float32
	offsetUpdated func(fyne.Position)

	windowFocused         func() bool
	threadActive          func() bool
	readCallback          func(uuid.UUID, string)
	unreadCountCallback   func(int)
	markAllAsReadCallback func()
	seenTracking          map[uuid.UUID]bool
}

func (ui *ui) newChatHistory(t thread) *chatHistory {
	_, group := t.(*group)
	ch := &chatHistory{
		id:                  t.getID(),
		myID:                ui.state.profile.id,
		messages:            ui.messages,
		added:               make(map[uuid.UUID]bool),
		ids:                 []uuid.UUID{},
		heights:             []float32{},
		windowFocused:       func() bool { return ui.state.focused },
		threadActive:        func() bool { return ui.isActive(t) },
		readCallback:        ui.bounce.MarkAsRead,
		unreadCountCallback: t.getButton().setUnreadCount,
		markAllAsReadCallback: func() {
			if group {
				ui.bounce.MarkAllGroupMessagesAsRead(t.getID())
			} else {
				ui.bounce.MarkAllDirectMessagesAsRead(t.getID())
			}
		},
		seenTracking: make(map[uuid.UUID]bool),
		template: func() fyne.CanvasObject {
			return container.NewStack(
				ui.newChatBubbleTemplate(),
				newStatusChangeTemplate(),
			)
		},
	}

	ch.jumpToBottomIcon = newClickableImage(
		"",
		canvas.NewImageFromResource(newEmbeddedResource("assets/icons/thread/jump_to_bottom.png")),
		jumpToBottomIconSize,
		jumpToBottomIconSize,
		false,
		func() {
			ch.ScrollToBottom()
			ch.unread = 0
			ch.updateUnreadCounter()
			for index, id := range ch.ids {
				item := ch.getItem(index)
				item.markSeen()
				ch.seenTracking[id] = true
			}
			ch.markAllAsReadCallback()
			if !fyne.CurrentDevice().IsMobile() {
				ui.window.Canvas().Focus(t.getEntry())
			}
		},
	)
	ch.jumpToBottomIcon.Hide()

	// Keep chat bubble timestamps up to date by periodically refreshing
	go func() {
		for {
			time.Sleep(timestampRefreshSeconds * time.Second)
			fyne.Do(func() { ch.Refresh() })
		}
	}()

	ch.ExtendBaseWidget(ch)
	return ch
}

func (ch *chatHistory) setItems(items []threadable, initialSize fyne.Size) {
	sizer := ch.template()

	ch.ids = []uuid.UUID{}
	for _, item := range items {
		ch.ids = append(ch.ids, item.getID())
		ch.messages.insert(item)
	}

	for i, id := range ch.ids {
		if _, ok := ch.added[id]; ok {
			log.WithFields(log.Fields{
				"id": id,
			}).Warn("ignoring duplicate thread item in setItems")
			continue
		}
		ch.added[id] = true

		item, _ := ch.messages.get(id)

		if item.isSeen() {
			ch.seenTracking[item.getID()] = true
		}

		if item.countsAsUnread() && !item.isSeen() && !(item.getAuthor() == ch.myID) {
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

			ch.getItem(i).populateTemplate(sizer)
			sizer.Refresh() // Unclear why this is required for smooth scrolling, widget should be updated already
			sizer.Resize(initialSize)
			height := sizer.MinSize().Height
			ch.SetItemHeight(i, height)
			ch.messages.cacheHeight(item.getID(), initialSize.Width, height)
		}
	}

	ch.updateUnreadCounter()
	ch.Refresh()
}

func (ch *chatHistory) insertItem(item threadable, appendingToEnd bool) {
	if _, ok := ch.added[item.getID()]; ok {
		log.WithFields(log.Fields{
			"id": item.getID(),
		}).Warn("ignoring duplicate thread item in insertItems")
		return
	}
	ch.added[item.getID()] = true

	ch.messages.insert(item)

	if len(ch.ids) == 0 || appendingToEnd {
		ch.ids = append(ch.ids, item.getID())
		ch.setMergeMode(len(ch.ids)-1, true)
	} else {
		for i := len(ch.ids); i >= 0; i-- {
			if i == 0 {
				ch.ids = append([]uuid.UUID{item.getID()}, ch.ids...)
				ch.heights = append([]float32{0}, ch.heights...)
				ch.setMergeMode(0, true)
				break
			}
			compare := ch.getItem(i - 1)
			if item.getTimestamp() > compare.getTimestamp() {
				ch.ids = append(ch.ids[:i], append([]uuid.UUID{item.getID()}, ch.ids[i:]...)...)
				if len(ch.heights) >= len(ch.ids)-1 {
					ch.heights = append(ch.heights[:i], append([]float32{0}, ch.heights[i:]...)...)
				}
				ch.setMergeMode(i, true)
				break
			}
		}
	}

	if item.countsAsUnread() && !item.isSeen() && !(item.getAuthor() == ch.myID) {
		ch.unread += 1
	}
	ch.updateUnreadCounter()
}

func (ch *chatHistory) getItem(index int) threadable {
	item, ok := ch.messages.get(ch.ids[index])
	if !ok {
		log.WithFields(log.Fields{
			"index": index,
		}).Fatal("message in chat history missing from message store")
	}
	return item
}

func (ch *chatHistory) deleteItem(id uuid.UUID) bool {
	found := false
	location := 0
	for i, itemID := range ch.ids {
		if itemID == id {
			found = true
			location = i
			break
		}
	}

	if found {
		ch.ids = append(ch.ids[:location], ch.ids[location+1:]...)
		ch.heights = append(ch.heights[:location], ch.heights[location+1:]...)
		ch.Refresh()
	}

	ch.messages.remove(id)

	return found
}

func (ch *chatHistory) isLastItem(id uuid.UUID) bool {
	if len(ch.ids) == 0 {
		return false
	}

	lastID := ch.ids[len(ch.ids)-1]
	return lastID == id
}

func (ch *chatHistory) isLastAuthor(id uuid.UUID) bool {
	if len(ch.ids) == 0 {
		return false
	}

	item := ch.getItem(len(ch.ids) - 1)
	return item.getAuthor() == id
}

func (ch *chatHistory) setMergeMode(index int, neighbors bool) {
	mergeUp := false
	mergeDown := false

	checkUp := true
	checkDown := true

	if index >= len(ch.ids) {
		log.WithFields(log.Fields{
			"index": index,
		}).Warn("out of bounds index while setting merge mode")
		return
	}
	if len(ch.ids)-1 == index {
		checkDown = false
	}
	if index == 0 {
		checkUp = false
	}

	item := ch.getItem(index)
	myType := item.getType()
	if !(myType == chat.TypeGroupMessage || myType == chat.TypeDirectMessage) {
		return
	}
	myAuthor := item.getAuthor()
	myTimestamp := item.getTimestamp()

	if checkUp {
		above := ch.getItem(index - 1)
		aboveType := above.getType()
		aboveAuthor := above.getAuthor()
		aboveTimestamp := above.getTimestamp()
		if (aboveType == chat.TypeGroupMessage || aboveType == chat.TypeDirectMessage) && aboveAuthor == myAuthor && aboveTimestamp > myTimestamp-mergeSeconds {
			mergeUp = true
		}
	}

	if checkDown {
		below := ch.getItem(index + 1)
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
			above := ch.getItem(index - 1)
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
			below := ch.getItem(index + 1)
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
	currentHead := int64(0)
	if len(ch.ids) > 0 {
		compare := ch.getItem(len(ch.ids) - 1)
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
	if ch.disableSeenTracking {
		return
	}

	id := ch.ids[index]
	if _, ok := ch.seenTracking[id]; !ok {
		ch.seenTracking[id] = true

		item := ch.getItem(index)
		item.markSeen()
		if item.countsAsUnread() && !(item.getAuthor() == ch.myID) {
			if ch.unread > 0 {
				ch.unread -= 1
			}
			ch.updateUnreadCounter()
		}
		if ch.windowFocused() {
			go ch.readCallback(id, item.getType())
		}
	}
}

func (ch *chatHistory) updateUnreadCounter() {
	ch.unreadCountCallback(ch.unread)
}

func (ch *chatHistory) MinSize() fyne.Size {
	ch.ExtendBaseWidget(ch)

	return fyne.Size{
		Height: ch.BaseWidget.MinSize().Height,
		Width:  imageAttachmentWidth + bufferSize + iconSize + theme.Padding()*6,
	}
}

func (ch *chatHistory) RefreshItem(index int) {
	if ch.scroller == nil {
		return
	}
	ch.BaseWidget.Refresh()
	lo := ch.scroller.Content.(*fyne.Container).Layout.(*chatHistoryLayout)
	item, ok := lo.searchVisible(lo.visible, index)
	if ok {
		lo.setupItem(item, index)
	}
}

func (ch *chatHistory) SetItemHeight(index int, height float32) {
	if !(len(ch.heights) > index) {
		missing := index + 1 - len(ch.heights)
		ch.heights = append(ch.heights, make([]float32, missing)...)
	}
	refresh := ch.heights[index] != height
	ch.heights[index] = height

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
		height, ok := ch.getItemHeight(i)

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
	ch.ScrollToOffset(ch.offsetFor(index))
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
	if id < 0 || id >= len(ch.ids) {
		return
	}
	ch.scrollTo(id)
	ch.Refresh()
}

func (ch *chatHistory) ScrollToBottom() {
	if ch.scroller == nil {
		return
	}
	ch.scroller.ScrollToBottom()
	ch.offsetUpdated(ch.scroller.Offset)
}

func (ch *chatHistory) ScrollToTop() {
	if ch.scroller == nil {
		return
	}
	ch.scroller.ScrollToTop()
	ch.offsetUpdated(ch.scroller.Offset)
}

func (ch *chatHistory) scrollToLastRead() {
	for i, _ := range ch.ids {
		item := ch.getItem(i)

		if !item.countsAsUnread() {
			continue
		}
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
	ch.scroller.ScrollToOffset(fyne.NewPos(0, offset))
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
	if scrollerHeight <= 0 {
		scrollerHeight = chatContainerSizeAtStartup().Height
	}
	if ch.contentHeight() > scrollerHeight*2.5 && ch.offsetY < ch.contentHeight()-scrollerHeight*2.5 {
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
		ch.offsetFor(len(ch.ids)-1)+scrollerHeight,
	)
}

func (ch *chatHistory) contentHeight() float32 {
	if ch.scroller == nil {
		return 0
	}
	return ch.scroller.Content.(*fyne.Container).Size().Height
}

func (ch *chatHistory) calculateAndSetItemHeight(index int, containerSize fyne.Size) float32 {
	sizer := ch.template()
	ch.getItem(index).populateTemplate(sizer)
	sizer.Refresh() // Unclear why this is required for smooth scrolling, widget should be updated already
	sizer.Resize(containerSize)
	height := sizer.MinSize().Height
	ch.SetItemHeight(index, height)
	if len(ch.ids) > index {
		ch.messages.cacheHeight(ch.ids[index], containerSize.Width, height)
	}
	return height
}

func (ch *chatHistory) CreateRenderer() fyne.WidgetRenderer {
	ch.ExtendBaseWidget(ch)

	layout := &fyne.Container{Layout: newChatHistoryLayout(ch)}
	ch.scroller = container.NewVScroll(layout)
	ch.scroller.OnScrolled = ch.offsetUpdated
	layout.Resize(layout.MinSize())
	return newChatHistoryRenderer(ch)
}

type chatHistoryRenderer struct {
	//cachedWidth  float32
	//cachedHeight float32
	ch      *chatHistory
	objects []fyne.CanvasObject
}

func newChatHistoryRenderer(ch *chatHistory) *chatHistoryRenderer {
	chr := &chatHistoryRenderer{
		//cachedWidth:  float32(fyne.CurrentApp().Preferences().Float("chat-width")),
		//cachedHeight: float32(fyne.CurrentApp().Preferences().Float("chat-height")),
		ch: ch,
		objects: []fyne.CanvasObject{
			ch.scroller,
			ch.jumpToBottomIcon,
		},
	}

	return chr
}

func (chr *chatHistoryRenderer) Layout(size fyne.Size) {
	chr.ch.scroller.Resize(size)
	if chr.ch.jumpToBottomIcon.Visible() {
		chr.ch.jumpToBottomIcon.Resize(fyne.Size{Width: jumpToBottomIconSize, Height: jumpToBottomIconSize})
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
	for _, obj := range chr.Objects() {
		obj.Refresh()
	}

	layout := chr.ch.scroller.Content.(*fyne.Container).Layout.(*chatHistoryLayout)
	layout.updateList(false)

	canvas.Refresh(chr.ch)
}

func (chr *chatHistoryRenderer) Destroy() {
}

func (chr chatHistoryRenderer) Objects() []fyne.CanvasObject {
	return chr.objects
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
}

func newChatHistoryLayout(ch *chatHistory) fyne.Layout {
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
		item = chl.ch.template()
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
			chl.ch.markAllAsReadCallback()
			chl.ch.unread = 0
			chl.ch.updateUnreadCounter()

			for index, id := range chl.ch.ids {
				if !chl.ch.disableSeenTracking {
					item := chl.ch.getItem(index)
					item.markSeen()
					chl.ch.seenTracking[id] = true
				}
			}
		}
	}
}

func (chl *chatHistoryLayout) setupItem(item fyne.CanvasObject, index int) {
	chl.ch.getItem(index).populateTemplate(item)
	chl.ch.SetItemHeight(index, item.MinSize().Height)
	if len(chl.ch.ids) > index {
		chl.ch.messages.cacheHeight(chl.ch.ids[index], chl.ch.Size().Width, item.MinSize().Height)
	}
}

func (chl *chatHistoryLayout) updateList(newOnly bool) {
	th := chl.ch.Theme()
	separatorThickness := th.Size(theme.SizeNamePadding)
	width := chl.ch.Size().Width
	length := len(chl.ch.ids)

	// Keep pointer reference for copying slice header when returning to the pool
	// https://blog.mike.norgate.xyz/unlocking-go-slice-performance-navigating-sync-pool-for-enhanced-efficiency-7cb63b0b453e
	wasVisiblePtr := chl.slicePool.Get().(*[]itemAndIndex)
	wasVisible := (*wasVisiblePtr)[:0]
	wasVisible = append(wasVisible, chl.visible...)

	offY, minRow := chl.calculateVisibleRowHeights(length, th)
	if len(chl.visibleRowHeights) == 0 && length > 0 { // we can't show anything until we have some dimensions
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

	if newOnly {
		for _, vis := range visible {
			if vis.index > len(chl.ch.ids)-1 {
				log.Warn("ignoring attempt to set up chat history item outside of bounds")
				return
			}
			if _, ok := chl.searchVisible(wasVisible, vis.index); !ok {
				chl.setupItem(vis.item, vis.index)
			}

			offset := chl.ch.GetScrollOffset()
			if offset >= chl.ch.offsetFor(vis.index) {
				if chl.ch.threadActive() && chl.ch.windowFocused() {
					chl.ch.seen(vis.index)
				}
			}
		}
	} else {
		for _, vis := range visible {
			if vis.index > len(chl.ch.ids)-1 {
				log.Warn("ignoring attempt to set up chat history item outside of bounds")
				return
			}
			chl.setupItem(vis.item, vis.index)

			offset := chl.ch.GetScrollOffset()
			if offset >= chl.ch.offsetFor(vis.index) {
				if chl.ch.threadActive() && chl.ch.windowFocused() {
					chl.ch.seen(vis.index)
				}
			}
		}

		// a full refresh may change theme, we should drain the pool of unused items instead of refreshing them.
		for chl.itemPool.Get() != nil {
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
