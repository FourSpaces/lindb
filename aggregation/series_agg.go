package aggregation

import (
	"sort"

	"github.com/lindb/lindb/aggregation/selector"
	"github.com/lindb/lindb/pkg/interval"
	"github.com/lindb/lindb/pkg/timeutil"
	"github.com/lindb/lindb/series"
)

//go:generate mockgen -source=./series_agg.go -destination=./series_agg_mock.go -package=aggregation

// defines series aggregates which aggregates fields of a time series
type FieldAggregates []SeriesAggregator

// ResultSet returns the result set of aggregator
func (agg FieldAggregates) ResultSet(tags map[string]string) series.GroupedIterator {
	return newGroupedIterator(tags, agg)
}

// Reset resets the aggregator's context for reusing
func (agg FieldAggregates) Reset() {
	for _, aggregator := range agg {
		aggregator.Reset()
	}
}

// NewFieldAggregates creates the field aggregates based on aggregator specs and query time range.
// NOTICE: if do down sampling aggregator, aggregator specs must be in order by field id.
func NewFieldAggregates(queryInterval int64, ratio int, queryTimeRange *timeutil.TimeRange,
	isDownSampling bool, aggSpecs AggregatorSpecs) FieldAggregates {
	aggregates := make(FieldAggregates, len(aggSpecs))
	for idx, aggSpec := range aggSpecs {
		aggregates[idx] = NewSeriesAggregator(queryInterval, ratio, queryTimeRange, isDownSampling, aggSpec)
	}
	if !isDownSampling {
		sort.Slice(aggregates, func(i, j int) bool {
			return aggregates[i].FieldName() < aggregates[j].FieldName()
		})
	}
	return aggregates
}

// SeriesAggregator represents a series aggregator which aggregates one field of a time series
type SeriesAggregator interface {
	// FieldName returns field name
	FieldName() string
	// GetAggregator gets field aggregator by segment start time, if not exist return (nil,false).
	GetAggregator(segmentStartTime int64) (FieldAggregator, bool)
	// Aggregates returns all field aggregates
	Aggregates() []FieldAggregator
	// ResultSet returns the result set of series aggregator
	ResultSet() series.Iterator
	// Reset resets the aggregator's context for reusing
	Reset()
}

type seriesAggregator struct {
	fieldName      string
	ratio          int
	isDownSampling bool
	aggregates     []FieldAggregator
	queryInterval  int64
	queryTimeRange *timeutil.TimeRange
	aggSpec        AggregatorSpec
	calc           interval.Calculator

	startTime int64
}

// NewSeriesAggregator creates a series aggregator
func NewSeriesAggregator(queryInterval int64, ratio int, queryTimeRange *timeutil.TimeRange, isDownSampling bool,
	aggSpec AggregatorSpec) SeriesAggregator {
	calc := interval.GetCalculator(interval.CalcIntervalType(queryInterval))
	segmentTime := calc.CalcSegmentTime(queryTimeRange.Start)
	startTime := calc.CalcFamilyStartTime(segmentTime, calc.CalcFamily(queryTimeRange.Start, segmentTime))

	length := calc.CalcTimeWindows(queryTimeRange.Start, queryTimeRange.End)
	agg := &seriesAggregator{
		fieldName:      aggSpec.FieldName(),
		startTime:      startTime,
		ratio:          ratio,
		isDownSampling: isDownSampling,
		calc:           calc,
		queryInterval:  queryInterval,
		queryTimeRange: queryTimeRange,
		aggSpec:        aggSpec,
	}
	if length > 0 {
		agg.aggregates = make([]FieldAggregator, length)
	}
	return agg
}

// FieldName returns field name
func (a *seriesAggregator) FieldName() string {
	return a.fieldName
}

// Aggregates returns all field aggregates
func (a *seriesAggregator) Aggregates() []FieldAggregator {
	return a.aggregates
}

// ResultSet returns the result set of series aggregator
func (a *seriesAggregator) ResultSet() series.Iterator {
	if len(a.aggregates) == 0 {
		return nil
	}
	return newSeriesIterator(a)
}

// Reset resets the aggregator's context for reusing
func (a *seriesAggregator) Reset() {
	for _, aggregator := range a.aggregates {
		if aggregator == nil {
			continue
		}
		aggregator.reset()
	}
}

// GetAggregator gets field aggregator by segment start time, if not exist return (nil,false).
func (a *seriesAggregator) GetAggregator(segmentStartTime int64) (agg FieldAggregator, ok bool) {
	if segmentStartTime < a.startTime {
		return
	}
	idx := a.calc.CalcTimeWindows(a.startTime, segmentStartTime) - 1
	if idx < 0 || idx >= len(a.aggregates) {
		return
	}
	agg = a.aggregates[idx]
	if agg == nil {
		storageTimeRange := &timeutil.TimeRange{
			Start: segmentStartTime,
			End:   a.calc.CalcFamilyEndTime(segmentStartTime),
		}
		timeRange := a.queryTimeRange.Intersect(storageTimeRange)
		storageInterval := a.queryInterval / int64(a.ratio)
		startIdx := a.calc.CalcSlot(timeRange.Start, segmentStartTime, storageInterval)
		endIdx := a.calc.CalcSlot(timeRange.End, segmentStartTime, storageInterval)
		if a.isDownSampling {
			agg = NewDownSamplingFieldAggregator(segmentStartTime,
				selector.NewIndexSlotSelector(startIdx, endIdx, a.ratio),
				a.aggSpec)
		} else {
			agg = NewFieldAggregator(segmentStartTime, selector.NewIndexSlotSelector(startIdx, endIdx, a.ratio),
				a.aggSpec)
		}
		a.aggregates[idx] = agg
	}
	ok = true
	return
}
