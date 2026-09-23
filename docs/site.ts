export const site = {
	name: 'bluecollar',
	tagline: 'An agent harness for work nobody is watching.',
	description:
		'bluecollar is an embeddable Go agent harness: the loop that takes a request, calls the tools a host hands it, proves completion from its own ledger, and reports failure to the person who asked.',
	origin: 'https://bluecollar.intern.kim',
	repository: { owner: 'yeomyeonggeori', name: 'bluecollar', branch: 'main' },
	color: { light: '#0e7490', dark: '#22d3ee' },
	gettingStarted: ['index', 'quickstart', 'architecture'],
	icons: {
		index: 'Compass',
		quickstart: 'Rocket',
		architecture: 'Layers',
		concepts: 'Shapes',
		contract: 'Plug',
		evaluation: 'Gauge',
		questions: 'MessageCircleQuestion',
	} as Record<string, string>,
	groupDescriptions: {} as Record<string, string>,
};
