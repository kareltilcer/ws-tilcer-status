// Single source of truth for route paths.
export const paths = {
  board: '/',
  addSite: '/add',
  site: (id: string) => `/sites/${encodeURIComponent(id)}`,
  sitePattern: '/sites/:id',
  group: (groupId: number) => `/crashes/${groupId}`,
  groupPattern: '/crashes/:groupId',
}
