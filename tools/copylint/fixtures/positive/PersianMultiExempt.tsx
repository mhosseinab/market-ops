// POSITIVE fixture — one reasoned allow directive on the FIRST Persian line must
// NOT exempt the whole file. Every Persian occurrence is checked independently;
// the exemption suppresses only the literal on its own (next) line, so the
// SECOND, non-exempt Persian literal still reports [persian].
// copylint-allow: first line reviewed as an approved verbatim state token
export const first = "تاییدشده";
export const second = "متناقض";
