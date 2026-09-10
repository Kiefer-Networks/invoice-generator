package auth

const (
	TransactionCookieName            = "__Host-invoice_oidc_transaction"
	DevelopmentTransactionCookieName = "invoice_oidc_transaction_dev"
)

const transactionCookieName = TransactionCookieName

func TransactionCookieNameForSecure(secure bool) string {
	if secure {
		return TransactionCookieName
	}
	return DevelopmentTransactionCookieName
}
