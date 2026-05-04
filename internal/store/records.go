package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"xorm.io/xorm"
)

type RequestRecordRepo struct {
	engine *xorm.Engine
}

func NewRequestRecordRepo(engine *xorm.Engine) *RequestRecordRepo {
	return &RequestRecordRepo{engine: engine}
}

func (r *RequestRecordRepo) Insert(ctx context.Context, record *domain.RequestRecord) error {
	_, err := r.engine.Context(ctx).Insert(record)
	if err != nil {
		return fmt.Errorf("insert request record: %w", err)
	}
	return nil
}

func (r *RequestRecordRepo) GetByID(ctx context.Context, id int64) (*domain.RequestRecord, error) {
	var record domain.RequestRecord
	ok, err := r.engine.Context(ctx).ID(id).Get(&record)
	if err != nil {
		return nil, fmt.Errorf("get request record %d: %w", id, err)
	}
	if !ok {
		return nil, domain.ErrRequestRecordNotFound
	}
	return &record, nil
}

func (r *RequestRecordRepo) Query(ctx context.Context, params core.QueryParams) ([]domain.RequestRecord, error) {
	var records []domain.RequestRecord
	sess := r.engine.Context(ctx)
	defer func() { _ = sess.Close() }()

	sess = applyRequestRecordSessionFilters(sess, params)
	if params.BeforeID != nil {
		sess = sess.Where("id < ?", *params.BeforeID)
	}
	if params.Limit > 0 {
		sess = sess.Limit(params.Limit)
	}
	sess = sess.OrderBy("id DESC")

	if err := sess.Find(&records); err != nil {
		return nil, fmt.Errorf("query request records: %w", err)
	}
	return records, nil
}

func (r *RequestRecordRepo) FilterOptions(ctx context.Context, params core.QueryParams) (core.RequestFilterOptions, error) {
	where, args := requestRecordWhereSQL(params)

	accountIDs, err := r.distinctInt64(ctx, "upstream_account_id", where, args)
	if err != nil {
		return core.RequestFilterOptions{}, fmt.Errorf("query request account options: %w", err)
	}
	outcomes, err := r.distinctStrings(ctx, "outcome", where, args)
	if err != nil {
		return core.RequestFilterOptions{}, fmt.Errorf("query request outcome options: %w", err)
	}
	models, err := r.distinctStrings(ctx, "model", where, args)
	if err != nil {
		return core.RequestFilterOptions{}, fmt.Errorf("query request model options: %w", err)
	}
	responseModes, err := r.distinctStrings(ctx, "response_mode", where, args)
	if err != nil {
		return core.RequestFilterOptions{}, fmt.Errorf("query request response mode options: %w", err)
	}

	return core.RequestFilterOptions{
		AccountIDs:    accountIDs,
		Outcomes:      outcomes,
		Models:        models,
		ResponseModes: responseModes,
	}, nil
}

func (r *RequestRecordRepo) UsageSummary(ctx context.Context) (core.RequestUsageSummary, error) {
	count, err := r.engine.Context(ctx).
		Count(new(domain.RequestRecord))
	if err != nil {
		return core.RequestUsageSummary{}, fmt.Errorf("count usage records: %w", err)
	}

	sess := r.engine.Context(ctx).
		Cols("token_usage").
		Where("token_usage IS NOT NULL")
	defer func() { _ = sess.Close() }()

	rows, err := sess.Rows(new(domain.RequestRecord))
	if err != nil {
		return core.RequestUsageSummary{}, fmt.Errorf("query usage records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	summary := core.RequestUsageSummary{RequestCount: int(count)}
	for rows.Next() {
		var record domain.RequestRecord
		if err := rows.Scan(&record); err != nil {
			return core.RequestUsageSummary{}, fmt.Errorf("scan usage record: %w", err)
		}
		tokens := dashboardTokens(record.TokenUsage)
		summary.TotalTokens += tokens.InputCached + tokens.InputNonCached + tokens.Output
		summary.CachedInputTokens += tokens.InputCached
	}
	return summary, nil
}

func (r *RequestRecordRepo) Dashboard(ctx context.Context, query core.DashboardQuery) (core.DashboardAggregation, error) {
	normalized, err := core.NormalizeDashboardQuery(query, time.Now().UTC())
	if err != nil {
		return core.DashboardAggregation{}, err
	}
	start, end, bucketDuration, err := core.DashboardWindow(normalized)
	if err != nil {
		return core.DashboardAggregation{}, err
	}
	bucketCount := dashboardBucketCount(start, end, bucketDuration)
	aggregation := newDashboardAggregation(normalized.Range, start, end, bucketDuration, bucketCount)
	bucketTTFT := make([][]int, bucketCount)
	allTTFT := make([]int, 0)

	sess := r.engine.Context(ctx).
		Cols("created_at", "upstream_account_id", "outcome", "token_usage", "ttft_ms").
		Where("created_at >= ?", start).
		And("created_at <= ?", end)
	defer func() { _ = sess.Close() }()
	if normalized.AccountID != nil {
		sess = sess.Where("upstream_account_id = ?", *normalized.AccountID)
	}

	rows, err := sess.Rows(new(domain.RequestRecord))
	if err != nil {
		return core.DashboardAggregation{}, fmt.Errorf("query dashboard records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var record domain.RequestRecord
		if err := rows.Scan(&record); err != nil {
			return core.DashboardAggregation{}, fmt.Errorf("scan dashboard record: %w", err)
		}
		idx := dashboardBucketIndex(record.CreatedAt, start, end, bucketDuration, bucketCount)
		if idx < 0 {
			continue
		}
		aggregation.Requests.Total++
		aggregation.Requests.Series[idx].Count++

		tokens := dashboardTokens(record.TokenUsage)
		aggregation.Tokens.Totals.InputCached += tokens.InputCached
		aggregation.Tokens.Totals.InputNonCached += tokens.InputNonCached
		aggregation.Tokens.Totals.Output += tokens.Output
		aggregation.Tokens.Series[idx].InputCached += tokens.InputCached
		aggregation.Tokens.Series[idx].InputNonCached += tokens.InputNonCached
		aggregation.Tokens.Series[idx].Output += tokens.Output

		aggregation.ErrorRate.Total++
		aggregation.ErrorRate.Series[idx].Total++
		if dashboardOutcomeIsError(record.Outcome) {
			aggregation.ErrorRate.Errors++
			aggregation.ErrorRate.Series[idx].Errors++
		}

		if record.TTFTMs != nil && *record.TTFTMs >= 0 {
			allTTFT = append(allTTFT, *record.TTFTMs)
			bucketTTFT[idx] = append(bucketTTFT[idx], *record.TTFTMs)
			aggregation.TTFT.SampleCount++
			aggregation.TTFT.Series[idx].SampleCount++
		}
	}

	aggregation.ErrorRate.Value = dashboardRate(aggregation.ErrorRate.Errors, aggregation.ErrorRate.Total)
	for i := range aggregation.ErrorRate.Series {
		aggregation.ErrorRate.Series[i].Rate = dashboardRate(aggregation.ErrorRate.Series[i].Errors, aggregation.ErrorRate.Series[i].Total)
	}
	aggregation.TTFT.P95MS = dashboardP95(allTTFT)
	for i := range aggregation.TTFT.Series {
		aggregation.TTFT.Series[i].P95MS = dashboardP95(bucketTTFT[i])
	}
	return aggregation, nil
}

func applyRequestRecordSessionFilters(sess *xorm.Session, params core.QueryParams) *xorm.Session {
	if params.Start != nil {
		sess = sess.Where("created_at >= ?", *params.Start)
	}
	if params.End != nil {
		sess = sess.Where("created_at <= ?", *params.End)
	}
	accountIDs := effectiveAccountIDs(params)
	if len(accountIDs) > 0 {
		sess = sess.In("upstream_account_id", accountIDs)
	} else if params.AccountID != nil {
		sess = sess.Where("upstream_account_id = ?", *params.AccountID)
	}
	outcomes := effectiveStrings(params.Outcomes)
	if len(outcomes) > 0 {
		sess = sess.In("outcome", outcomes)
	} else if params.Outcome != nil {
		sess = sess.Where("outcome = ?", *params.Outcome)
	}
	if models := effectiveStrings(params.Models); len(models) > 0 {
		sess = sess.In("model", models)
	}
	if modes := effectiveStrings(params.ResponseModes); len(modes) > 0 {
		sess = sess.In("response_mode", modes)
	}
	if params.Search != nil {
		pattern := likePattern(*params.Search)
		sess = sess.Where(requestRecordSearchClause(), pattern, pattern, pattern, pattern, pattern, pattern, pattern)
	}
	return sess
}

func newDashboardAggregation(
	rangeValue core.DashboardRange,
	start time.Time,
	end time.Time,
	bucketDuration time.Duration,
	bucketCount int,
) core.DashboardAggregation {
	aggregation := core.DashboardAggregation{
		Range:         rangeValue,
		WindowStart:   start,
		WindowEnd:     end,
		BucketSeconds: int(bucketDuration.Seconds()),
		Requests: core.DashboardRequestsCard{
			Series: make([]core.DashboardCountPoint, bucketCount),
		},
		Tokens: core.DashboardTokensCard{
			Series: make([]core.DashboardTokenPoint, bucketCount),
		},
		ErrorRate: core.DashboardErrorRateCard{
			Series: make([]core.DashboardRatePoint, bucketCount),
		},
		TTFT: core.DashboardTTFTCard{
			Series: make([]core.DashboardTTFTPoint, bucketCount),
		},
	}
	for i := 0; i < bucketCount; i++ {
		timestamp := start.Add(time.Duration(i) * bucketDuration)
		aggregation.Requests.Series[i].Timestamp = timestamp
		aggregation.Tokens.Series[i].Timestamp = timestamp
		aggregation.ErrorRate.Series[i].Timestamp = timestamp
		aggregation.TTFT.Series[i].Timestamp = timestamp
	}
	return aggregation
}

func dashboardBucketCount(start, end time.Time, bucketDuration time.Duration) int {
	if bucketDuration <= 0 || !end.After(start) {
		return 1
	}
	count := int(end.Sub(start) / bucketDuration)
	if start.Add(time.Duration(count) * bucketDuration).Before(end) {
		count++
	}
	if count < 1 {
		return 1
	}
	return count
}

func dashboardBucketIndex(createdAt, start, end time.Time, bucketDuration time.Duration, bucketCount int) int {
	if createdAt.Before(start) || createdAt.After(end) || bucketDuration <= 0 || bucketCount <= 0 {
		return -1
	}
	if createdAt.Equal(end) {
		return bucketCount - 1
	}
	idx := int(createdAt.Sub(start) / bucketDuration)
	if idx < 0 {
		return -1
	}
	if idx >= bucketCount {
		return bucketCount - 1
	}
	return idx
}

func dashboardTokens(usage domain.JSONMap) core.DashboardTokenBreakdown {
	input := dashboardUsageInt(usage, "input", "input_tokens")
	cached := dashboardUsageInt(usage, "cached_input", "cached_tokens")
	if cached == 0 {
		cached = dashboardNestedUsageInt(usage, "input_tokens_details", "cached_tokens")
	}
	output := dashboardUsageInt(usage, "output", "output_tokens")
	nonCached := input - cached
	if nonCached < 0 {
		nonCached = 0
	}
	return core.DashboardTokenBreakdown{
		InputCached:    cached,
		InputNonCached: nonCached,
		Output:         output,
	}
}

func dashboardUsageInt(usage domain.JSONMap, keys ...string) int {
	for _, key := range keys {
		if value, ok := dashboardAnyToInt(usage[key]); ok {
			return value
		}
	}
	return 0
}

func dashboardNestedUsageInt(usage domain.JSONMap, objectKey string, valueKey string) int {
	raw := usage[objectKey]
	switch value := raw.(type) {
	case map[string]any:
		if got, ok := dashboardAnyToInt(value[valueKey]); ok {
			return got
		}
	case domain.JSONMap:
		if got, ok := dashboardAnyToInt(value[valueKey]); ok {
			return got
		}
	}
	return 0
}

func dashboardAnyToInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v >= 0
	case int64:
		if v < 0 {
			return 0, false
		}
		return int(v), true
	case float64:
		if v < 0 {
			return 0, false
		}
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		if err != nil || i < 0 {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func dashboardOutcomeIsError(outcome string) bool {
	switch outcome {
	case domain.OutcomeSuccess, domain.OutcomeNoExtractableText:
		return false
	default:
		return true
	}
}

func dashboardRate(errorsCount, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(errorsCount) / float64(total)
}

func dashboardP95(values []int) *int {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	idx := (95*len(sorted) + 99) / 100
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	value := sorted[idx-1]
	return &value
}

func (r *RequestRecordRepo) distinctInt64(ctx context.Context, column, where string, args []any) ([]int64, error) {
	sql := fmt.Sprintf(
		"SELECT DISTINCT %s FROM request_records%s AND %s IS NOT NULL ORDER BY %s ASC",
		column,
		where,
		column,
		column,
	)
	sess := r.engine.Context(ctx)
	defer func() { _ = sess.Close() }()
	rows, err := sess.QueryString(queryStringArgs(sql, args)...)
	if err != nil {
		return nil, err
	}
	values := make([]int64, 0, len(rows))
	for _, row := range rows {
		raw := row[column]
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse distinct %s %q: %w", column, raw, err)
		}
		values = append(values, value)
	}
	return values, nil
}

func (r *RequestRecordRepo) distinctStrings(ctx context.Context, column, where string, args []any) ([]string, error) {
	sql := fmt.Sprintf(
		"SELECT DISTINCT %s FROM request_records%s AND %s IS NOT NULL AND %s != '' ORDER BY %s ASC",
		column,
		where,
		column,
		column,
		column,
	)
	sess := r.engine.Context(ctx)
	defer func() { _ = sess.Close() }()
	rows, err := sess.QueryString(queryStringArgs(sql, args)...)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if value := row[column]; value != "" {
			values = append(values, value)
		}
	}
	return values, nil
}

func requestRecordWhereSQL(params core.QueryParams) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	if params.Start != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, *params.Start)
	}
	if params.End != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, *params.End)
	}
	accountIDs := effectiveAccountIDs(params)
	if len(accountIDs) > 0 {
		clauses = append(clauses, "upstream_account_id IN ("+placeholders(len(accountIDs))+")")
		for _, id := range accountIDs {
			args = append(args, id)
		}
	} else if params.AccountID != nil {
		clauses = append(clauses, "upstream_account_id = ?")
		args = append(args, *params.AccountID)
	}
	outcomes := effectiveStrings(params.Outcomes)
	if len(outcomes) > 0 {
		clauses = append(clauses, "outcome IN ("+placeholders(len(outcomes))+")")
		for _, outcome := range outcomes {
			args = append(args, outcome)
		}
	} else if params.Outcome != nil {
		clauses = append(clauses, "outcome = ?")
		args = append(args, *params.Outcome)
	}
	if models := effectiveStrings(params.Models); len(models) > 0 {
		clauses = append(clauses, "model IN ("+placeholders(len(models))+")")
		for _, model := range models {
			args = append(args, model)
		}
	}
	if modes := effectiveStrings(params.ResponseModes); len(modes) > 0 {
		clauses = append(clauses, "response_mode IN ("+placeholders(len(modes))+")")
		for _, mode := range modes {
			args = append(args, mode)
		}
	}
	if params.Search != nil {
		pattern := likePattern(*params.Search)
		clauses = append(clauses, requestRecordSearchClause())
		args = append(args, pattern, pattern, pattern, pattern, pattern, pattern, pattern)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func requestRecordSearchClause() string {
	return `(LOWER(request_id) LIKE ? ESCAPE '!' OR LOWER(path) LIKE ? ESCAPE '!' OR LOWER(model) LIKE ? ESCAPE '!' OR LOWER(session_key) LIKE ? ESCAPE '!' OR LOWER(method) LIKE ? ESCAPE '!' OR LOWER(error_code) LIKE ? ESCAPE '!' OR LOWER(client_ip) LIKE ? ESCAPE '!')`
}

func effectiveAccountIDs(params core.QueryParams) []int64 {
	if len(params.AccountIDs) == 0 {
		return nil
	}
	out := make([]int64, 0, len(params.AccountIDs))
	seen := map[int64]struct{}{}
	for _, id := range params.AccountIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func effectiveStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func likePattern(value string) string {
	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(strings.TrimSpace(value)))
	return "%" + escaped + "%"
}

func queryStringArgs(query string, args []any) []any {
	sqlArgs := make([]any, 0, len(args)+1)
	sqlArgs = append(sqlArgs, query)
	sqlArgs = append(sqlArgs, args...)
	return sqlArgs
}

func (r *RequestRecordRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	sess := r.engine.Context(ctx)
	defer func() { _ = sess.Close() }()

	var ids []int64
	err := sess.Table("request_records").
		Cols("id").
		Where("created_at < ?", before).
		OrderBy("id ASC").
		Limit(1000).
		Find(&ids)
	if err != nil {
		return 0, fmt.Errorf("select old request records: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	affected, err := r.engine.Context(ctx).In("id", ids).Delete(new(domain.RequestRecord))
	if err != nil {
		return 0, fmt.Errorf("delete old request records: %w", err)
	}
	return affected, nil
}
