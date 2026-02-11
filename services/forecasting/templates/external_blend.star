# external_blend.star - Blend internal and external forecast template.
#
# Combines internal historical observations with external forecast data using
# a configurable weight. The default blend is 70% internal / 30% external.
#
# ForecastContext builtins used: avg, add_seconds
#
# Reference data attributes (optional):
#   - internal_weight: Weight for internal forecast (0-1, default: 0.7)
#
# Input: Requires exactly 2 input datasets:
#   - First dataset (alphabetically): Internal observations
#   - Second dataset (alphabetically): External forecast observations
#
# Output: Weighted blend of both sources projected across the horizon.

def compute_forecast(ctx):
    obs_dict = ctx["observations"]
    dataset_keys = sorted(obs_dict.keys())

    if len(dataset_keys) < 2:
        # Fall back to single source if only one dataset available
        if len(dataset_keys) == 0:
            return []
        obs = obs_dict[dataset_keys[0]]
        if len(obs) == 0:
            return []
        values = [float(o["value"]) for o in obs]
        # avg() returns a Decimal; convert via str() for float arithmetic
        forecast_value = float(str(avg(values)))
    else:
        internal_obs = obs_dict[dataset_keys[0]]
        external_obs = obs_dict[dataset_keys[1]]

        # Return empty if both datasets are empty
        if len(internal_obs) == 0 and len(external_obs) == 0:
            return []

        # Determine internal weight from reference data, clamp to [0, 1]
        ref = ctx["reference_data"]
        internal_weight = 0.7
        if ref != None:
            attrs = ref["attributes"]
            if "internal_weight" in attrs:
                internal_weight = float(attrs["internal_weight"])
        if internal_weight < 0.0 or internal_weight > 1.0:
            internal_weight = 0.7

        external_weight = 1.0 - internal_weight

        # Compute averages for each source (avg returns Decimal, convert via str)
        if len(internal_obs) > 0:
            internal_values = [float(o["value"]) for o in internal_obs]
            internal_avg = float(str(avg(internal_values)))
        else:
            internal_avg = 0.0

        if len(external_obs) > 0:
            external_values = [float(o["value"]) for o in external_obs]
            external_avg = float(str(avg(external_values)))
        else:
            external_avg = 0.0

        # Renormalize weights based on data availability
        if len(internal_obs) == 0:
            internal_weight = 0.0
        if len(external_obs) == 0:
            external_weight = 0.0
        total_weight = internal_weight + external_weight
        if total_weight > 0.0:
            internal_weight = internal_weight / total_weight
            external_weight = external_weight / total_weight

        forecast_value = internal_avg * internal_weight + external_avg * external_weight

    now = ctx["now"]
    granularity = int(ctx["granularity_seconds"])
    if granularity <= 0:
        return []
    horizon = int(ctx["horizon_seconds"])
    num_points = horizon // granularity

    points = []
    for i in range(num_points):
        offset = (i + 1) * granularity
        ts = add_seconds(now, offset)
        points.append({
            "timestamp": ts,
            "value": forecast_value,
            "metadata": {"algorithm": "external_blend"},
        })
    return points
