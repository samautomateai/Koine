export { Type } from "./typebox.mjs";
export const StringEnum = (values, o) => ({ type: "string", enum: [...values], ...(o || {}) });
export const Model = undefined;
export const Message = undefined;
export default {};
