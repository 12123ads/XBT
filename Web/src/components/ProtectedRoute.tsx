import { Navigate, Outlet } from 'react-router-dom';
import { useAuthStore } from '../store/auth';

const ProtectedRoute = () => {
  const { isAuthenticated, activeUid, token } = useAuthStore();

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />;
  }

  return <Outlet key={`${activeUid}:${token}`} />;
};

export default ProtectedRoute;
