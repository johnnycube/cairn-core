// Shared activity-classification option lists for the create/edit forms.
// Mirrors domain.AllActivityTypes (the lowercase identifiers) and the
// discipline sub-splits. Types not listed in DISCIPLINES have no sub-split.

export const ACTIVITY_TYPES = [
	'ride', 'run', 'swim', 'hike', 'walk', 'row', 'ski', 'workout',
	'snowboard', 'skate', 'kayak', 'sup', 'surf', 'golf', 'climb', 'tennis',
	'elliptical', 'wheelchair'
];

export const DISCIPLINES: Record<string, string[]> = {
	ride: ['ride_road', 'ride_mtb', 'ride_gravel', 'ride_cyclocross', 'ride_track', 'ride_bmx'],
	run: ['run_road', 'run_trail', 'run_track'],
	swim: ['swim_pool', 'swim_open_water'],
	ski: ['ski_alpine', 'ski_nordic', 'ski_touring', 'ski_backcountry']
};

// Human label for an enum-ish identifier ("ride_road" → "ride road").
export function typeLabel(s: string): string {
	return s.replace(/_/g, ' ');
}
