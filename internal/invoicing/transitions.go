package invoicing

import (
	"context"
	"time"
)

func (s *FinalizationService) MarkPaid(ctx context.Context, id string) (FinalizedInvoice, error) {
	f, err := s.store.InvoiceRepository().Transition(ctx, id, "paid", "")
	if err != nil {
		return FinalizedInvoice{}, err
	}
	return decodeFinalized(f)
}
func (s *FinalizationService) Cancel(ctx context.Context, id, reason string) (FinalizedInvoice, error) {
	f, err := s.store.InvoiceRepository().Transition(ctx, id, "cancelled", reason)
	if err != nil {
		return FinalizedInvoice{}, err
	}
	return decodeFinalized(f)
}
func (s *FinalizationService) MarkOverdue(ctx context.Context) (int, error) {
	return s.store.InvoiceRepository().MarkOverdue(ctx, time.Now().UTC())
}
func (s *FinalizationService) CreateCorrection(ctx context.Context, id string) (Draft, error) {
	d, err := s.store.InvoiceRepository().CreateCorrection(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	return toDraft(d)
}
