export function page(items, pageNum, size) {
  const start = pageNum * size;
  return items.slice(start, start + size);
}
