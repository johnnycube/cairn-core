// Decode a Google-encoded polyline into coordinates. Default precision is 5.
// Returns [lat, lon] pairs; callers that feed MapLibre/GeoJSON should swap to
// [lon, lat].
export function decodePolyline(encoded: string, precision = 5): [number, number][] {
	if (!encoded) return [];
	const factor = Math.pow(10, precision);
	const coords: [number, number][] = [];
	let index = 0;
	let lat = 0;
	let lng = 0;

	while (index < encoded.length) {
		let result = 0;
		let shift = 0;
		let b: number;
		do {
			b = encoded.charCodeAt(index++) - 63;
			result |= (b & 0x1f) << shift;
			shift += 5;
		} while (b >= 0x20);
		lat += result & 1 ? ~(result >> 1) : result >> 1;

		result = 0;
		shift = 0;
		do {
			b = encoded.charCodeAt(index++) - 63;
			result |= (b & 0x1f) << shift;
			shift += 5;
		} while (b >= 0x20);
		lng += result & 1 ? ~(result >> 1) : result >> 1;

		coords.push([lat / factor, lng / factor]);
	}
	return coords;
}

// decodePolylineLonLat returns [lon, lat] pairs ready for MapLibre / GeoJSON.
export function decodePolylineLonLat(encoded: string, precision = 5): [number, number][] {
	return decodePolyline(encoded, precision).map(([lat, lon]) => [lon, lat]);
}
