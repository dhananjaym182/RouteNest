export default {
  id: "beta",
  category: "freeTier",
  display: { name: "Beta" },
  transport: { format: "openai", baseUrls: ["https://beta.example/v1/responses", "https://unused.example"] },
  models: [{ id: "beta-embedding-v1", kind: "embedding" }, { id: "beta-chat" }],
};
