# seasonal_decomposition.star - Seasonal decomposition forecast template.
#
# Decomposes historical observations into hour-of-day patterns using medians
# for outlier robustness. Projects the seasonal pattern across the forecast
# horizon.
#
# ForecastContext builtins used: group_by_hour, avg, add_seconds
#
# Input: Historical observations with timestamps spanning multiple days.
# Output: Forecast points reflecting the discovered hourly seasonal pattern.

def compute_forecast(ctx):
    obs_dict = ctx["observations"]

    dataset_keys = sorted(obs_dict.keys())
    if len(dataset_keys) == 0:
        return []

    obs = obs_dict[dataset_keys[0]]
    if len(obs) == 0:
        return []

    # Group observations by hour-of-day
    hourly_groups = group_by_hour(obs)

    # Compute median for each hour (outlier robust)
    hourly_medians = {}
    for hour in hourly_groups.keys():
        group = hourly_groups[hour]
        values = sorted([float(o["value"]) for o in group])
        n = len(values)
        if n == 0:
            continue
        if n % 2 == 1:
            median = values[n // 2]
        else:
            median = (values[n // 2 - 1] + values[n // 2]) / 2.0
        hourly_medians[int(hour)] = median

    # If no medians computed, fall back to overall average
    # avg() returns a Decimal; convert via str() for float arithmetic
    if len(hourly_medians) == 0:
        all_values = [float(o["value"]) for o in obs]
        fallback = float(str(avg(all_values)))
        hourly_medians = {h: fallback for h in range(24)}

    # Compute global average for hours without data
    known_values = [hourly_medians[h] for h in hourly_medians.keys()]
    global_avg = 0.0
    for v in known_values:
        global_avg = global_avg + float(str(v))
    global_avg = global_avg / len(known_values)

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts_str = add_seconds(now, offset)

        # Parse hour from the RFC3339 timestamp (format: ...THH:MM:SS...)
        t_index = ts_str.index("T")
        hour = int(ts_str[t_index + 1 : t_index + 3])

        if hour in hourly_medians:
            value = hourly_medians[hour]
        else:
            value = global_avg

        points.append({
            "timestamp": ts_str,
            "value": value,
            "metadata": {"algorithm": "seasonal_decomposition", "hour": str(hour)},
        })
    return points
