package auth

const (
	TransactionCookieName            = "__Host-invoice_oidc_transaction"
	DevelopmentTransactionCookieName = "invoice_oidc_transaction_dev"
)

func TransactionCookieNameForSecure(secure bool) string {
	if secure {
		return TransactionCookieName
	}
	return DevelopmentTransactionCookieName
}
