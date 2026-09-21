declare module "*.yaml" {
  const data: { api?: { base_url?: string; timeout?: number } };
  export default data;
}
