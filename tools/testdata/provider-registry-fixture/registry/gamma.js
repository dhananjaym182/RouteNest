export default {
  id: "gamma",
  category: "apikey",
  hasFree: true,
  display: { name: "Gamma Local" },
  transport: { format: "openai", baseUrl: "http://localhost:9000/v1" },
  models: [{ id: "gamma-7b" }],
};
