const noop = () => new Proxy(function () {}, { get: () => noop(), apply: async () => ({ data: {} }) });
export const createOpencodeClient = () => noop();
export const createOpencode = async () => ({ client: noop(), server: { url: "http://localhost:0", close() {} } });
export const Client = undefined;
export const Event = undefined;
export default {};
