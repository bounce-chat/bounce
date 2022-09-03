package ui

import (
	"time"

	"github.com/hako/durafmt"
)

type retentionSelection struct {
	display string
	value   int64
}

var retentionOneHour = retentionSelection{
	display: "1 Hour",
	value:   int64(time.Duration(1 * time.Hour).Seconds()),
}

var retentionOneDay = retentionSelection{
	display: "1 Day",
	value:   int64(time.Duration(24 * time.Hour).Seconds()),
}

var retentionOneWeek = retentionSelection{
	display: "1 Week",
	value:   int64(time.Duration(7 * 24 * time.Hour).Seconds()),
}

var retentionOneMonth = retentionSelection{
	display: "1 Month",
	value:   int64(time.Duration(4 * 7 * 24 * time.Hour).Seconds()),
}

var retentionOff = retentionSelection{
	display: "Off",
	value:   0,
}

var retentionSelections = []string{retentionOneHour.display, retentionOneDay.display, retentionOneWeek.display, retentionOneMonth.display, retentionOff.display}
var retentionValues = map[string]int64{
	retentionOneHour.display:  retentionOneHour.value,
	retentionOneDay.display:   retentionOneDay.value,
	retentionOneWeek.display:  retentionOneWeek.value,
	retentionOneMonth.display: retentionOneMonth.value,
	retentionOff.display:      retentionOff.value,
}
var retentionNames = map[int64]string{
	retentionOneHour.value:  retentionOneHour.display,
	retentionOneDay.value:   retentionOneDay.display,
	retentionOneWeek.value:  retentionOneWeek.display,
	retentionOneMonth.value: retentionOneMonth.display,
	retentionOff.value:      retentionOff.display,
}

func getRetentionName(retention int64) string {
	name, ok := retentionNames[retention]
	if ok {
		return name
	}
	return durafmt.Parse(time.Duration(retention) * time.Second).String() // TODO: language support
}
