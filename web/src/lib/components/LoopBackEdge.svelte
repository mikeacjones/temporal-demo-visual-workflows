<script lang="ts">
	import { BaseEdge, getSmoothStepPath, Position, type EdgeProps } from '@xyflow/svelte';

	// "Next item" / "next pass": from the end of a body back into its Loop or
	// For each. It goes round the far side of the loop from its body's box
	// (TB: left of it; LR: above it), so it never crosses the box.
	let { sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, label, labelStyle, style, markerStart, markerEnd, interactionWidth }: EdgeProps = $props();

	// Clear of a loop card, which is at most this far from its centre line.
	const CLEAR = 160;
	let [path, labelX, labelY] = $derived(
		getSmoothStepPath({
			sourceX,
			sourceY,
			targetX,
			targetY,
			sourcePosition,
			targetPosition,
			...(targetPosition === Position.Top ? { centerX: targetX - CLEAR } : { centerY: targetY - CLEAR / 2 })
		})
	);
</script>

<BaseEdge {path} {labelX} {labelY} {label} {labelStyle} {style} {markerStart} {markerEnd} {interactionWidth} />
