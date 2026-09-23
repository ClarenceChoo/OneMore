# Explainable discovery

The **Soonest** feed remains chronological and filterable. **For you** selects up to 200 upcoming candidate plans, loads their current membership and capacity in one database query, scores them, and displays the top 24. It excludes plans hosted by the viewer. Both views continue to use current plan state from PostgreSQL.

## Rule-based score

| Signal | Points |
| --- | ---: |
| Category is in saved interests | 5 |
| Area matches the saved preferred area | 3 |
| Start time fits weekday evenings or weekends in the saved time zone | 3 |
| Recent clicks or joins in the same category | Up to 3 |
| A space is currently open | 1 |

The strongest matching preference becomes the explanation shown with the plan. Ties break by start time and stable plan ID. With no preferences or interaction history, the view stays close to chronological order. This is a transparent heuristic, not a learned recommendation model. The first candidate pass prioritizes matching categories and area before start time; its 200-plan limit is a deliberate performance bound and may miss a later plan that would have scored highly on availability.

## Evaluating usefulness

The browser records an impression when at least half of a card enters the viewport, and records clicks with `soonest` or `for_you` as the source. A successful join writes an event in the same transaction as membership and attributes it to a click on that plan in the preceding 24 hours, or to `direct` otherwise. Events are aggregated per member, plan, kind, and source. The data supports directional comparisons of click-through and join rates, with the caveat that people choose their own feed view and the groups are not randomized.

For a basic comparison in an isolated or appropriately accessed database:

```sql
SELECT source,
       sum(count) FILTER (WHERE kind='impression') AS impressions,
       sum(count) FILTER (WHERE kind='click') AS clicks,
       sum(count) FILTER (WHERE kind='join') AS joins
FROM discovery_events
WHERE last_at >= now() - interval '30 days'
GROUP BY source
ORDER BY source;
```

These totals are only instrumentation. No lift or adoption claim should be made without sufficient traffic, a defined cohort, and a controlled comparison.
