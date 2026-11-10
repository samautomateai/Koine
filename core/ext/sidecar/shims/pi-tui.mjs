// Rendering primitives are accepted but inert: koine draws tool output itself.
class Node { constructor(...a) { this.args = a; this.children = []; } addChild(c) { this.children.push(c); } render() { return []; } invalidate() {} }
export class Text extends Node {}
export class Container extends Node {}
export class Spacer extends Node {}
export class Box extends Node {}
export class Markdown extends Node {}
export const matchesKey = () => false;
export const Key = {};
export const truncateToWidth = (s, w) => String(s).slice(0, w);
export const visibleWidth = (s) => String(s).length;
export default {};
