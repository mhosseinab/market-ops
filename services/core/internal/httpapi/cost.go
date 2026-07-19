package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	gateway "github.com/mhosseinab/market-ops/gen/go"
	"github.com/mhosseinab/market-ops/services/core/internal/cost"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
)

// CostService is the cost-profile / CSV import / readiness orchestration the
// gateway depends on (PRD §7.2 CST-001..003). *cost.Service satisfies it. It is
// an interface so the transport can be tested with a fake and httpapi stays free
// of DB wiring.
type CostService interface {
	PreviewImport(ctx context.Context, in cost.PreviewInput) (cost.Preview, error)
	GetPreview(ctx context.Context, batchID uuid.UUID) (cost.Preview, error)
	CommitImport(ctx context.Context, batchID, createdBy uuid.UUID) (cost.CommitResult, error)
	EnterSingleCost(ctx context.Context, in cost.SingleCostInput) (db.CostProfile, error)
	CostProfileAt(ctx context.Context, variant uuid.UUID, at time.Time) ([]db.CostProfile, error)
	GetReadiness(ctx context.Context, variant uuid.UUID) (db.MarginReadiness, error)
}

// PreviewCostImport builds a CSV import preview (CST-001). No cost value commits.
func (s *gatewayServer) PreviewCostImport(
	ctx context.Context, req gateway.PreviewCostImportRequestObject,
) (gateway.PreviewCostImportResponseObject, error) {
	if s.cost == nil {
		return gateway.PreviewCostImportdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.PreviewCostImportdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	in := cost.PreviewInput{
		Account:   req.Body.MarketplaceAccountId,
		Content:   req.Body.Csv,
		Mapping:   mappingFromRequest(req.Body.SkuColumn, req.Body.ComponentColumns),
		CreatedBy: principalID(ctx),
	}
	if req.Body.Filename != nil {
		in.Filename = *req.Body.Filename
	}
	preview, err := s.cost.PreviewImport(ctx, in)
	if err != nil {
		return gateway.PreviewCostImportdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	return gateway.PreviewCostImport200JSONResponse(toGatewayPreview(preview)), nil
}

// GetCostImportPreview re-fetches a stored preview batch (CST-001).
func (s *gatewayServer) GetCostImportPreview(
	ctx context.Context, req gateway.GetCostImportPreviewRequestObject,
) (gateway.GetCostImportPreviewResponseObject, error) {
	if s.cost == nil {
		return gateway.GetCostImportPreviewdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	preview, err := s.cost.GetPreview(ctx, req.Params.BatchId)
	if err != nil {
		return gateway.GetCostImportPreviewdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	return gateway.GetCostImportPreview200JSONResponse(toGatewayPreview(preview)), nil
}

// CommitCostImport commits a confirmed preview batch (CST-001/CST-002).
func (s *gatewayServer) CommitCostImport(
	ctx context.Context, req gateway.CommitCostImportRequestObject,
) (gateway.CommitCostImportResponseObject, error) {
	if s.cost == nil {
		return gateway.CommitCostImportdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.CommitCostImportdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	res, err := s.cost.CommitImport(ctx, req.Body.BatchId, principalID(ctx))
	if err != nil {
		return gateway.CommitCostImportdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	ids := make([]uuid.UUID, 0, len(res.AffectedVariants))
	ids = append(ids, res.AffectedVariants...)
	return gateway.CommitCostImport200JSONResponse(gateway.CostImportCommitResult{
		BatchId:            res.Batch.ID,
		Status:             gateway.CostImportCommitResultStatusCommitted,
		CommittedRows:      res.CommittedRows,
		AffectedVariantIds: ids,
	}), nil
}

// EnterSingleCost records one component value (CST-002, guided blocker flow).
func (s *gatewayServer) EnterSingleCost(
	ctx context.Context, req gateway.EnterSingleCostRequestObject,
) (gateway.EnterSingleCostResponseObject, error) {
	if s.cost == nil {
		return gateway.EnterSingleCostdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	if req.Body == nil {
		return gateway.EnterSingleCostdefaultJSONResponse{StatusCode: 400, Body: invalidArgErr("request body is required")}, nil
	}
	in := cost.SingleCostInput{
		Account:   req.Body.MarketplaceAccountId,
		VariantID: req.Body.VariantId,
		Component: cost.Component(req.Body.Component),
		RawValue:  req.Body.RawValue,
		CreatedBy: principalID(ctx),
	}
	if req.Body.RawUnit != nil {
		in.RawUnit = *req.Body.RawUnit
	}
	if req.Body.EffectiveFrom != nil {
		in.EffectiveFrom = *req.Body.EffectiveFrom
	}
	if req.Body.StaleAfter != nil {
		t := *req.Body.StaleAfter
		in.StaleAfter = &t
	}
	profile, err := s.cost.EnterSingleCost(ctx, in)
	if err != nil {
		return gateway.EnterSingleCostdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	return gateway.EnterSingleCost200JSONResponse(toGatewayCostProfile(profile)), nil
}

// ListCostProfiles returns the in-force version per component at a time (CST-002).
func (s *gatewayServer) ListCostProfiles(
	ctx context.Context, req gateway.ListCostProfilesRequestObject,
) (gateway.ListCostProfilesResponseObject, error) {
	if s.cost == nil {
		return gateway.ListCostProfilesdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	at := time.Now()
	if req.Params.AsOf != nil {
		at = *req.Params.AsOf
	}
	rows, err := s.cost.CostProfileAt(ctx, req.Params.VariantId, at)
	if err != nil {
		return gateway.ListCostProfilesdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	out := make([]gateway.CostProfileVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, toGatewayCostProfile(r))
	}
	return gateway.ListCostProfiles200JSONResponse(gateway.CostProfileList{Items: out}), nil
}

// GetMarginReadiness returns a SKU's derived readiness (CST-003).
func (s *gatewayServer) GetMarginReadiness(
	ctx context.Context, req gateway.GetMarginReadinessRequestObject,
) (gateway.GetMarginReadinessResponseObject, error) {
	if s.cost == nil {
		return gateway.GetMarginReadinessdefaultJSONResponse{StatusCode: 503, Body: costUnavailableErr()}, nil
	}
	row, err := s.cost.GetReadiness(ctx, req.Params.VariantId)
	if err != nil {
		return gateway.GetMarginReadinessdefaultJSONResponse{StatusCode: costStatus(err), Body: costErr(err)}, nil
	}
	return gateway.GetMarginReadiness200JSONResponse(toGatewayReadiness(row)), nil
}

// --- mapping helpers -------------------------------------------------------

func mappingFromRequest(skuColumn *string, cols *[]gateway.ColumnComponentMapping) cost.Mapping {
	m := cost.Mapping{}
	if skuColumn != nil {
		m.SKUColumn = *skuColumn
	}
	if cols != nil && len(*cols) > 0 {
		m.Components = make(map[string]cost.Component, len(*cols))
		for _, c := range *cols {
			m.Components[c.Header] = cost.Component(c.Component)
		}
	}
	return m
}

func toGatewayPreview(p cost.Preview) gateway.CostImportPreview {
	out := gateway.CostImportPreview{
		BatchId:              p.Batch.ID,
		MarketplaceAccountId: p.Batch.MarketplaceAccountID,
		Status:               gateway.CostImportPreviewStatus(p.Batch.Status),
		Counts: gateway.CostImportCounts{
			Accept:    int(p.Batch.AcceptCount),
			Reject:    int(p.Batch.RejectCount),
			Duplicate: int(p.Batch.DuplicateCount),
		},
		Rows: make([]gateway.CostImportRow, 0, len(p.Rows)),
	}
	if p.Batch.Filename != "" {
		f := p.Batch.Filename
		out.Filename = &f
	}
	if p.Detected.SKUColumn != "" || len(p.Detected.ComponentColumns) > 0 {
		out.Detected = toGatewayDetected(p.Detected)
	}
	for _, r := range p.Rows {
		out.Rows = append(out.Rows, toGatewayImportRow(r))
	}
	return out
}

func toGatewayDetected(d cost.DetectedMapping) *gateway.DetectedMapping {
	cols := make([]gateway.ColumnComponentMapping, 0, len(d.ComponentColumns))
	for _, c := range d.ComponentColumns {
		cols = append(cols, gateway.ColumnComponentMapping{Header: c.Header, Component: gateway.CostComponent(c.Component)})
	}
	return &gateway.DetectedMapping{SkuColumn: d.SKUColumn, ComponentColumns: cols}
}

func toGatewayImportRow(r db.CostImportRow) gateway.CostImportRow {
	row := gateway.CostImportRow{
		RowNumber:       int(r.RowNumber),
		Sku:             r.RawSku,
		Component:       gateway.CostComponent(r.Component),
		RawValue:        r.RawValue,
		NormalizedValue: r.NormalizedValue,
		Disposition:     gateway.CostImportDisposition(r.Disposition),
		Reason:          r.Reason,
	}
	if r.ResolvedVariantID.Valid {
		id := uuid.UUID(r.ResolvedVariantID.Bytes)
		row.VariantId = &id
	}
	if r.AmountMantissa.Valid {
		row.Amount = &gateway.MoneyAmount{
			Mantissa: wireMantissa(r.AmountMantissa.Int64),
			Currency: r.AmountCurrency,
			Exponent: int(r.AmountExponent),
		}
	}
	return row
}

func toGatewayCostProfile(p db.CostProfile) gateway.CostProfileVersion {
	out := gateway.CostProfileVersion{
		Id:                   p.ID,
		MarketplaceAccountId: p.MarketplaceAccountID,
		VariantId:            p.VariantID,
		Component:            gateway.CostComponent(p.Component),
		Version:              int(p.Version),
		Amount: gateway.MoneyAmount{
			Mantissa: wireMantissa(p.AmountMantissa),
			Currency: p.AmountCurrency,
			Exponent: int(p.AmountExponent),
		},
		EffectiveFrom: p.EffectiveFrom,
		Source:        gateway.CostProfileVersionSource(p.Source),
	}
	if p.RawText != "" {
		v := p.RawText
		out.RawText = &v
	}
	if p.RawValue != "" {
		v := p.RawValue
		out.RawValue = &v
	}
	if p.RawUnit != "" {
		v := p.RawUnit
		out.RawUnit = &v
	}
	if p.StaleAfter.Valid {
		t := p.StaleAfter.Time
		out.StaleAfter = &t
	}
	return out
}

func toGatewayReadiness(r db.MarginReadiness) gateway.MarginReadiness {
	return gateway.MarginReadiness{
		VariantId:            r.VariantID,
		MarketplaceAccountId: r.MarketplaceAccountID,
		State:                gateway.MarginReadinessState(r.State),
		MissingComponents:    decodeComponents(r.MissingComponents),
		StaleComponents:      decodeComponents(r.StaleComponents),
		ComputedAt:           r.ComputedAt,
	}
}

func decodeComponents(raw []byte) []gateway.CostComponent {
	out := []gateway.CostComponent{}
	for _, c := range cost.DecodeComponentList(raw) {
		out = append(out, gateway.CostComponent(c))
	}
	return out
}

// principalID returns the authenticated principal's user id, or uuid.Nil when
// unavailable (the CreatedBy attribution is best-effort audit metadata).
func principalID(ctx context.Context) uuid.UUID {
	if p, ok := principalFrom(ctx); ok {
		return p.UserID
	}
	return uuid.Nil
}

func costStatus(err error) int {
	switch {
	case errors.Is(err, cost.ErrBatchNotFound), errors.Is(err, cost.ErrVariantNotFound):
		return 404
	case errors.Is(err, cost.ErrAccountVariantMismatch):
		// Tenant boundary breach: the supplied account does not own the variant.
		return 403
	case errors.Is(err, cost.ErrBatchNotPreview), errors.Is(err, cost.ErrUnresolvedDuplicates):
		return 409
	case errors.Is(err, cost.ErrNotUTF8), errors.Is(err, cost.ErrEmptyCSV),
		errors.Is(err, cost.ErrNoSKUColumn), errors.Is(err, cost.ErrNoComponentColumn),
		errors.Is(err, cost.ErrMalformedCSV), errors.Is(err, cost.ErrEmptyAmount),
		errors.Is(err, cost.ErrInvalidAmount), errors.Is(err, cost.ErrNegativeAmount),
		errors.Is(err, cost.ErrTooManyDecimals), errors.Is(err, cost.ErrPercentNotMoney):
		return 400
	default:
		return 500
	}
}

func costErr(err error) gateway.ErrorEnvelope {
	code := "COST_ERROR"
	switch {
	case errors.Is(err, cost.ErrBatchNotFound):
		code = "BATCH_NOT_FOUND"
	case errors.Is(err, cost.ErrVariantNotFound):
		code = "VARIANT_NOT_FOUND"
	case errors.Is(err, cost.ErrAccountVariantMismatch):
		code = "ACCOUNT_VARIANT_MISMATCH"
	case errors.Is(err, cost.ErrBatchNotPreview):
		code = "BATCH_NOT_PREVIEW"
	case errors.Is(err, cost.ErrUnresolvedDuplicates):
		code = "UNRESOLVED_DUPLICATES"
	case errors.Is(err, cost.ErrNotUTF8), errors.Is(err, cost.ErrEmptyCSV),
		errors.Is(err, cost.ErrNoSKUColumn), errors.Is(err, cost.ErrNoComponentColumn),
		errors.Is(err, cost.ErrMalformedCSV):
		code = "INVALID_CSV"
	case errors.Is(err, cost.ErrEmptyAmount), errors.Is(err, cost.ErrInvalidAmount),
		errors.Is(err, cost.ErrNegativeAmount), errors.Is(err, cost.ErrTooManyDecimals):
		code = "INVALID_AMOUNT"
	case errors.Is(err, cost.ErrPercentNotMoney):
		// Distinct code for disposition parity with the CSV preview reason
		// percent_not_money: a percentage is not Money (#40, §9.1).
		code = "PERCENT_NOT_MONEY"
	}
	return gateway.ErrorEnvelope{Code: code, Message: err.Error()}
}

func costUnavailableErr() gateway.ErrorEnvelope {
	return gateway.ErrorEnvelope{Code: "COST_UNAVAILABLE", Message: "cost service is not configured"}
}
