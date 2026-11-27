const {
  window: { localStorage },
} = global;

const local = {
  clear: (): void => {
    localStorage.clear();
  },
  getItem: (itemName: string): string | null => {
    return localStorage.getItem(`AUTOMDM::${itemName}`);
  },
  setItem: (itemName: string, value: string): void => {
    return localStorage.setItem(`AUTOMDM::${itemName}`, value);
  },
  removeItem: (itemName: string): void => {
    localStorage.removeItem(`AUTOMDM::${itemName}`);
  },
};

export default local;
