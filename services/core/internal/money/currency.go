package money

// ISO-4217 currency validation. The currency of a Money value is a three-letter
// alphabetic ISO-4217 code (PRD §9.1). This plane is locale/region-neutral
// (LOC-001): the code is data, and no region is privileged here — IRR is simply
// one recognised code among the active set. Cross-currency conversion does not
// exist in P0, so no minor-unit/display metadata is attached to the code; the
// exponent is carried explicitly on Money instead.

// activeISO4217 is the set of active ISO-4217 alphabetic codes (including the
// standard fund and precious-metal codes). It is intentionally a plain data
// table, not a locale branch. Add codes here if a market requires one; never
// special-case a single code in logic.
var activeISO4217 = map[string]struct{}{
	"AED": {}, "AFN": {}, "ALL": {}, "AMD": {}, "ANG": {}, "AOA": {}, "ARS": {},
	"AUD": {}, "AWG": {}, "AZN": {}, "BAM": {}, "BBD": {}, "BDT": {}, "BGN": {},
	"BHD": {}, "BIF": {}, "BMD": {}, "BND": {}, "BOB": {}, "BOV": {}, "BRL": {},
	"BSD": {}, "BTN": {}, "BWP": {}, "BYN": {}, "BZD": {}, "CAD": {}, "CDF": {},
	"CHE": {}, "CHF": {}, "CHW": {}, "CLF": {}, "CLP": {}, "CNY": {}, "COP": {},
	"COU": {}, "CRC": {}, "CUC": {}, "CUP": {}, "CVE": {}, "CZK": {}, "DJF": {},
	"DKK": {}, "DOP": {}, "DZD": {}, "EGP": {}, "ERN": {}, "ETB": {}, "EUR": {},
	"FJD": {}, "FKP": {}, "GBP": {}, "GEL": {}, "GHS": {}, "GIP": {}, "GMD": {},
	"GNF": {}, "GTQ": {}, "GYD": {}, "HKD": {}, "HNL": {}, "HTG": {}, "HUF": {},
	"IDR": {}, "ILS": {}, "INR": {}, "IQD": {}, "IRR": {}, "ISK": {}, "JMD": {},
	"JOD": {}, "JPY": {}, "KES": {}, "KGS": {}, "KHR": {}, "KMF": {}, "KPW": {},
	"KRW": {}, "KWD": {}, "KYD": {}, "KZT": {}, "LAK": {}, "LBP": {}, "LKR": {},
	"LRD": {}, "LSL": {}, "LYD": {}, "MAD": {}, "MDL": {}, "MGA": {}, "MKD": {},
	"MMK": {}, "MNT": {}, "MOP": {}, "MRU": {}, "MUR": {}, "MVR": {}, "MWK": {},
	"MXN": {}, "MXV": {}, "MYR": {}, "MZN": {}, "NAD": {}, "NGN": {}, "NIO": {},
	"NOK": {}, "NPR": {}, "NZD": {}, "OMR": {}, "PAB": {}, "PEN": {}, "PGK": {},
	"PHP": {}, "PKR": {}, "PLN": {}, "PYG": {}, "QAR": {}, "RON": {}, "RSD": {},
	"RUB": {}, "RWF": {}, "SAR": {}, "SBD": {}, "SCR": {}, "SDG": {}, "SEK": {},
	"SGD": {}, "SHP": {}, "SLE": {}, "SLL": {}, "SOS": {}, "SRD": {}, "SSP": {},
	"STN": {}, "SVC": {}, "SYP": {}, "SZL": {}, "THB": {}, "TJS": {}, "TMT": {},
	"TND": {}, "TOP": {}, "TRY": {}, "TTD": {}, "TWD": {}, "TZS": {}, "UAH": {},
	"UGX": {}, "USD": {}, "USN": {}, "UYI": {}, "UYU": {}, "UYW": {}, "UZS": {},
	"VED": {}, "VES": {}, "VND": {}, "VUV": {}, "WST": {}, "XAF": {}, "XAG": {},
	"XAU": {}, "XBA": {}, "XBB": {}, "XBC": {}, "XBD": {}, "XCD": {}, "XDR": {},
	"XOF": {}, "XPD": {}, "XPF": {}, "XPT": {}, "XSU": {}, "XTS": {}, "XUA": {},
	"XXX": {}, "YER": {}, "ZAR": {}, "ZMW": {}, "ZWL": {},
}

// ValidCurrency reports whether code is a recognised active ISO-4217 alphabetic
// code. The check is exact and case-sensitive: callers normalise casing at the
// input boundary, not here.
func ValidCurrency(code string) bool {
	_, ok := activeISO4217[code]
	return ok
}
