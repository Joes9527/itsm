import { fireEvent, render, screen, waitFor } from '@/lib/test-utils';
import LicenseForm from '../LicenseForm';
import { AssetApi, type License } from '@/lib/api/asset-api';

jest.unmock('dayjs');

const push = jest.fn();

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1101' }),
  useRouter: () => ({ push }),
}));

jest.mock('@/lib/api/asset-api', () => ({
  AssetApi: {
    getLicense: jest.fn(),
    updateLicense: jest.fn(),
    createLicense: jest.fn(),
  },
}));

const getLicense = AssetApi.getLicense as jest.Mock;
const updateLicense = AssetApi.updateLicense as jest.Mock;

const license: License = {
  id: 1101,
  name: '协作套件',
  licenseType: 'subscription',
  totalQuantity: 50,
  usedQuantity: 22,
  availableQuantity: 28,
  tenantId: 1,
  purchaseDate: '2026-01-10T00:00:00.000Z',
  expiryDate: '2027-01-10T00:00:00.000Z',
  status: 'active',
  createdAt: '2026-01-10T00:00:00.000Z',
  updatedAt: '2026-09-09T00:00:00.000Z',
};

describe('LicenseForm date adaptation', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    getLicense.mockResolvedValue(license);
    updateLicense.mockResolvedValue(license);
  });

  it('hydrates API date strings into DatePicker controls and preserves ISO output', async () => {
    render(<LicenseForm />);

    expect(await screen.findByDisplayValue('2026-01-10')).toBeInTheDocument();
    expect(screen.getByDisplayValue('2027-01-10')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(updateLicense).toHaveBeenCalledTimes(1));
    expect(updateLicense).toHaveBeenCalledWith(
      1101,
      expect.objectContaining({
        purchaseDate: '2026-01-10T00:00:00.000Z',
        expiryDate: '2027-01-10T00:00:00.000Z',
      })
    );
  });

  it('renders omitted optional dates as empty DatePicker controls', async () => {
    getLicense.mockResolvedValue({ ...license, purchaseDate: undefined, expiryDate: undefined });

    render(<LicenseForm />);

    await waitFor(() => expect(getLicense).toHaveBeenCalledWith(1101));
    expect(screen.getByPlaceholderText('选择采购日期')).toHaveValue('');
    expect(screen.getByPlaceholderText('选择到期日期')).toHaveValue('');
  });
});
