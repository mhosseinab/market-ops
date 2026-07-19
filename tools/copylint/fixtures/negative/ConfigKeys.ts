// NEGATIVE fixture — config holds catalog KEYS, not copy. Display properties
// (label, header) carrying a dotted key form are references, and the `*Key`
// convention is a reference by name.
export const routes = [
  { id: "today", path: "/today", titleKey: "route.today.title", header: "needsReview.col.sku" },
  { id: "market", label: "nav.market", navLabelKey: "nav.market" },
];
