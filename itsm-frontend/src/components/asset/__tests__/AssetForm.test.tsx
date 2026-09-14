import { fireEvent, render, screen, waitFor } from '@/lib/test-utils';
import AssetForm from '../AssetForm';
import { AssetApi, type Asset } from '@/lib/api/asset-api';

// The global lightweight dayjs mock cannot drive Ant Design's real picker.
jest.unmock('dayjs');

const push = jest.fn();

jest.mock('next/navigation', () => ({
  useParams: () => ({ id: '1001' }),
  useRouter: () => ({ push }),
}));

jest.mock('@/lib/api/asset-api', () => ({
  AssetApi: {
    getAsset: jest.fn(),
    updateAsset: jest.fn(),
    createAsset: jest.fn(),
  },
}));

const getAsset = AssetApi.getAsset as jest.Mock;
const updateAsset = AssetApi.updateAsset as jest.Mock;

const asset: Asset = {
  id: 1001,
  assetNumber: 'AST-1001',
  name: '标准笔记本',
  type: 'hardware',
  status: 'in-use',
  tenantId: 1,
  purchaseDate: '2026-01-10T00:00:00.000Z',
  warrantyExpiry: '2029-01-10T00:00:00.000Z',
  supportExpiry: '2028-01-10T00:00:00.000Z',
  createdAt: '2026-01-10T00:00:00.000Z',
  updatedAt: '2026-09-09T00:00:00.000Z',
};

describe('AssetForm date adaptation', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    getAsset.mockResolvedValue(asset);
    updateAsset.mockResolvedValue(asset);
  });

  it('hydrates API date strings into the three actual DatePicker controls and preserves ISO output', async () => {
    render(<AssetForm />);

    expect(await screen.findByDisplayValue('2026-01-10')).toBeInTheDocument();
    expect(screen.getByDisplayValue('2029-01-10')).toBeInTheDocument();
    expect(screen.getByDisplayValue('2028-01-10')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(updateAsset).toHaveBeenCalledTimes(1));
    expect(updateAsset).toHaveBeenCalledWith(
      1001,
      expect.objectContaining({
        purchaseDate: '2026-01-10T00:00:00.000Z',
        warrantyExpiry: '2029-01-10T00:00:00.000Z',
        supportExpiry: '2028-01-10T00:00:00.000Z',
      })
    );
  });

  it('renders omitted optional dates as empty DatePicker controls', async () => {
    getAsset.mockResolvedValue({
      ...asset,
      purchaseDate: undefined,
      warrantyExpiry: undefined,
      supportExpiry: undefined,
    });

    render(<AssetForm />);

    await waitFor(() => expect(getAsset).toHaveBeenCalledWith(1001));
    expect(screen.getByPlaceholderText('选择采购日期')).toHaveValue('');
    expect(screen.getByPlaceholderText('选择保修到期日期')).toHaveValue('');
    expect(screen.getByPlaceholderText('选择支持到期日期')).toHaveValue('');
  });
});
