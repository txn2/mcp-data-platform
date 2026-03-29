function StoreComparison() {
  const stores = [
    { name: "Downtown Flagship", revenue: 412500, traffic: 12400, conversion: 55.2, trend: "up" },
    { name: "Mall of America", revenue: 385200, traffic: 15800, conversion: 45.1, trend: "up" },
    { name: "Westfield Center", revenue: 298700, traffic: 9200, conversion: 59.6, trend: "up" },
    { name: "Airport Terminal B", revenue: 276400, traffic: 18300, conversion: 26.9, trend: "down" },
    { name: "University District", revenue: 245100, traffic: 11500, conversion: 51.2, trend: "up" },
    { name: "Harbor Mall", revenue: 234800, traffic: 8900, conversion: 52.8, trend: "flat" },
    { name: "Tech Park Plaza", revenue: 221300, traffic: 7600, conversion: 64.8, trend: "up" },
    { name: "Suburban Square", revenue: 198500, traffic: 6800, conversion: 58.4, trend: "down" },
    { name: "Riverside Walk", revenue: 187200, traffic: 7200, conversion: 52.0, trend: "up" },
    { name: "Market Street", revenue: 176900, traffic: 8100, conversion: 43.7, trend: "flat" },
  ];

  const maxRevenue = Math.max(...stores.map(s => s.revenue));

  const fmt = (n) => "$" + (n / 1000).toFixed(0) + "K";
  const trendIcon = (t) => t === "up" ? "↑" : t === "down" ? "↓" : "→";
  const trendColor = (t) => t === "up" ? "#16a34a" : t === "down" ? "#dc2626" : "#64748b";

  return (
    <div style={{ fontFamily: "-apple-system, sans-serif", padding: 24, background: "#f8fafc", minHeight: "100%" }}>
      <h2 style={{ fontSize: 18, fontWeight: 600, marginBottom: 4 }}>Store Performance Comparison</h2>
      <p style={{ fontSize: 13, color: "#64748b", marginBottom: 20 }}>Top 10 stores — Week ending Mar 28, 2026</p>
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {stores.map((store, i) => (
          <div key={i} style={{ display: "flex", alignItems: "center", gap: 12, background: "white", borderRadius: 8, padding: "12px 16px", boxShadow: "0 1px 2px rgba(0,0,0,0.06)" }}>
            <span style={{ width: 24, fontSize: 13, fontWeight: 600, color: "#64748b" }}>#{i + 1}</span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 14, fontWeight: 500, marginBottom: 4 }}>{store.name}</div>
              <div style={{ height: 6, background: "#e2e8f0", borderRadius: 3, overflow: "hidden" }}>
                <div style={{ height: "100%", width: `${(store.revenue / maxRevenue) * 100}%`, background: "#3b82f6", borderRadius: 3 }} />
              </div>
            </div>
            <div style={{ textAlign: "right", minWidth: 80 }}>
              <div style={{ fontSize: 14, fontWeight: 600 }}>{fmt(store.revenue)}</div>
              <div style={{ fontSize: 11, color: "#64748b" }}>revenue</div>
            </div>
            <div style={{ textAlign: "right", minWidth: 60 }}>
              <div style={{ fontSize: 14 }}>{(store.traffic / 1000).toFixed(1)}K</div>
              <div style={{ fontSize: 11, color: "#64748b" }}>traffic</div>
            </div>
            <div style={{ textAlign: "right", minWidth: 50 }}>
              <div style={{ fontSize: 14 }}>{store.conversion}%</div>
              <div style={{ fontSize: 11, color: "#64748b" }}>conv.</div>
            </div>
            <span style={{ fontSize: 16, color: trendColor(store.trend), width: 20, textAlign: "center" }}>
              {trendIcon(store.trend)}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

export default StoreComparison;
