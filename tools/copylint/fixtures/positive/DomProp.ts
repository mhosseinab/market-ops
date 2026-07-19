// POSITIVE fixture — vanilla-DOM display-property assignment flags [dom-prop].
export function paint(el: HTMLElement): void {
  el.textContent = "Hello there";
  el.title = "Tooltip copy";
}
