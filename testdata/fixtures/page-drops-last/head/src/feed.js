import { page } from './paginate.js';
export function firstPage(items, size) {
  return page(items, 0, size);
}
