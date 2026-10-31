export default {
  id: "alpha",
  category: "free",
  noAuth: true,
  display: { name: "Alpha" },
  transport: {
    format: "openai",
    baseUrl: "https://alpha.example/v1/chat/completions",
    headers: { "X-Test": "1", accept: "application/json" },
  },
  models: [{ id: "zeta-model" }, { id: "alpha-free" }, { id: "zeta-model" }],
};
