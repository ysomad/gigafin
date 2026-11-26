// Draws analytics trends with uPlot: income vs expense bars per month with a net worth
// line on its own scale and right axis. Each figure.trend holds its series as JSON, see
// ui.trendChart.
(() => {
  const halfMonth = 15 * 24 * 60 * 60;
  let charts = [];
  // phones get a lower chart with slimmer axes, which else take half its width
  const narrow = (width) => width < 500;
  const height = (width) => (narrow(width) ? 180 : 220);

  // Resolve Oat's light-dark() tokens through an element's computed color;
  // custom properties themselves still contain expressions unsuitable for canvas.
  function colors() {
    const probe = document.createElement('span');
    probe.hidden = true;
    document.body.append(probe);
    const palette = {};
    for (const name of ['success', 'danger', 'primary', 'border', 'muted-foreground']) {
      probe.style.color = `var(--${name})`;
      palette[name] = getComputedStyle(probe).color;
    }
    probe.remove();
    return palette;
  }

  function bars(color, align, full) {
    return {
      stroke: color,
      fill: color,
      width: 0,
      paths: uPlot.paths.bars({ size: [0.4, 40], align }),
      points: { show: false },
      value: (u, v) => (v == null ? '' : full.format(v)),
    };
  }

  function render() {
    charts.forEach((c) => c.destroy());
    charts = [];
    const figures = document.querySelectorAll('figure.trend');
    if (!figures.length) return;
    const palette = colors();
    figures.forEach((fig) => {
      // Figure attributes arrive with every htmx response, including after settings change.
      const locale = fig.dataset.locale || document.documentElement.lang || undefined;
      const compactFormat = new Intl.NumberFormat(locale, { notation: 'compact' });
      // Russian thousands are "тыс.": "т." leaves the plot more room
      const compact = (v) => compactFormat.format(v).replace('тыс.', 'т.');
      const full = new Intl.NumberFormat(locale, {
        maximumFractionDigits: fig.hasAttribute('data-minor-units') ? 2 : 0,
      });
      const d = JSON.parse(fig.querySelector('script').textContent);
      // translated series labels, see internal/ui/analytics/analytics.templ
      const label = fig.dataset;
      const networth = d.balance != null;
      const axis = { stroke: palette['muted-foreground'], grid: { stroke: palette.border, width: 1 }, ticks: { show: false } };
      // With no cursor over the chart the legend shows the last (current) month.
      const current = (u) => { if (u.cursor.idx == null) u.setLegend({ idx: u.data[0].length - 1 }); };
      const small = narrow(fig.clientWidth);
      // an axis is as wide as its longest label, as in uPlot's axis autosize demo
      const size = (u, values, i, cycle) => {
        const a = u.axes[i];
        if (cycle > 1) return a._size;
        const longest = (values ?? []).reduce((acc, v) => (v.length > acc.length ? v : acc), '');
        u.ctx.font = a.font[0];
        return Math.ceil(a.ticks.size + a.gap + u.ctx.measureText(longest).width / devicePixelRatio);
      };
      const chart = new uPlot({
        width: fig.clientWidth,
        height: height(fig.clientWidth),
        cursor: { points: { show: false }, drag: { x: false, y: false } },
        hooks: { ready: [current], setCursor: [current] },
        // the net worth line has no fill: give every legend marker its series color
        legend: { markers: { fill: (u, i) => u.series[i].stroke(u, i) } },
        scales: { x: { time: true, range: (u, min, max) => [min - halfMonth, max + halfMonth] } },
        axes: [
          { ...axis, grid: { show: false }, splits: (u) => (small ? u.data[0].filter((_, i, ts) => (ts.length - 1 - i) % 2 === 0) : u.data[0]), values: (u, ts) => ts.map((t) => new Date(t * 1000).toLocaleDateString(locale, { month: 'short', timeZone: 'UTC' })) },
          { ...axis, size, values: (u, vs) => vs.map(compact) },
          // net worth dwarfs a month of income or expense: its own scale, no grid
          ...(networth ? [{ ...axis, scale: 'networth', side: 1, size, grid: { show: false }, values: (u, vs) => vs.map(compact) }] : []),
        ],
        series: [
          {},
          { label: label.labelIncome || 'Income', ...bars(palette.success, -1, full) },
          { label: label.labelExpense || 'Expense', ...bars(palette.danger, 1, full) },
          ...(networth ? [{ label: label.labelBalance || 'Net worth', scale: 'networth', stroke: palette.primary, width: 2, points: { size: 5 }, value: (u, v) => (v == null ? '' : full.format(v)) }] : []),
        ],
      }, networth ? [d.months, d.income, d.expense, d.balance] : [d.months, d.income, d.expense], fig);
      // the x axis already names the months: drop the month row from the legend
      chart.root.querySelector('.u-legend .u-series')?.remove();
      charts.push(chart);
    });
  }

  document.addEventListener('DOMContentLoaded', render);
  // a swap without charts, such as a toast, keeps the drawn ones; those it removed
  // from the page are destroyed
  document.addEventListener('htmx:after:settle', (e) => {
    if (e.target.querySelector?.('figure.trend')) {
      render();
      return;
    }
    charts = charts.filter((c) => {
      if (!c.root.isConnected) c.destroy();
      return c.root.isConnected;
    });
  });
  // colors are read from CSS once per render: redraw when the OS theme flips
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', render);
  window.addEventListener('resize', () => {
    charts.forEach((c) => {
      const width = c.root.parentElement.clientWidth;
      c.setSize({ width, height: height(width) });
    });
  });
})();
