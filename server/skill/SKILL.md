---
name: healthsync
description: Answer questions about the user's Apple Health data and medical checkups with the HealthSync MCP server (steps, heart rate, HRV, sleep, workouts, weight, lab results). Use when the user asks about their health data, trends, sleep, workouts or checkup reports.
---

# HealthSync

The `healthsync` MCP server gives read-only access to the user's Apple Health data and
checkup reports. The data comes from the user's iPhone and stays on their own computer.

## Rules

- Get every number from the tools. Do not guess.
- Give the date range and the unit with each number.
- Say when data is missing or sparse, for example days without a watch.
- Do not diagnose. For a medical decision, tell the user to ask a doctor.
- Answer in the language that the user writes in.

## Start

1. Call `list_data_types`. It gives each type, its unit and its first and last day.
2. Select the tool for the question:

| Question | Tool |
|---|---|
| A trend over days or weeks | `daily_summary` (type, from, to) |
| Single samples, for example heart rate during a run | `get_records` (type, from, to, limit) |
| Checkup results | `get_checkups` (name, from) |
| Anything else | `query_sql` (one SELECT) |

Type names can be short: `StepCount`, `RestingHeartRate`, `HeartRateVariabilitySDNN`,
`BodyMass`, `SleepAnalysis`, `ActiveEnergyBurned`.

## SQL

Read the `healthsync://schema` resource for all columns. The most useful views contain only
samples that are not deleted:

- `daily(type, day, unit, total, mean, min, max, n)`: use `total` for sums (steps, energy)
  and `mean` for rates (heart rate, HRV).
- `v_resting_heart_rate(start_at, bpm)`, `v_hrv(start_at, ms)`, `v_weight(start_at, kg)`.
- `v_sleep(start_at, end_at, stage)`: one row for each sleep stage interval.
- `v_workouts(start_at, activity, duration_s, distance_km, active_energy_kcal, avg_heart_rate)`.

If you query `records` directly, add `WHERE deleted_at IS NULL`.

Examples:

```sql
-- Average resting heart rate per week
SELECT strftime('%Y-%W', start_at) AS week, ROUND(AVG(bpm), 1) AS bpm
FROM v_resting_heart_rate GROUP BY week ORDER BY week DESC LIMIT 12;

-- The sleep stage names. Check them before you filter.
SELECT stage, COUNT(*) FROM v_sleep GROUP BY stage;

-- Time asleep per night (all stages except in bed and awake), in hours
SELECT substr(end_at, 1, 10) AS night,
       ROUND(SUM((julianday(end_at) - julianday(start_at)) * 24), 2) AS hours
FROM v_sleep
WHERE lower(stage) NOT LIKE '%bed%' AND lower(stage) NOT LIKE '%awake%'
GROUP BY night ORDER BY night DESC LIMIT 14;

-- Workouts this month
SELECT start_at, activity, ROUND(duration_s / 60) AS minutes, distance_km
FROM v_workouts WHERE start_at >= date('now', 'start of month') ORDER BY start_at;
```

## Prompts and resources

The server also has prompts (`weekly_review`, `checkup_review`, `sleep_analysis`,
`compare_periods`) and resources (`healthsync://summary/7d`, `healthsync://checkups/latest`,
`healthsync://daily/{type}`). Use them as a start for the same kind of question.

## Turned off

If a call fails with "turned off", the user has turned off AI assistant access in
HealthSync. Tell the user to turn it on in the HealthSync app or dashboard.
